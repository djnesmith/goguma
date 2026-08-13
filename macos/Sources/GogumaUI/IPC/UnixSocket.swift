import Darwin
import Foundation

/// One blocking request/response round trip over an `AF_UNIX` stream socket.
///
/// Deliberately connection-per-call: the daemon's own CLI works the same way,
/// each call is a single small frame, and a fresh connection means a wedged
/// daemon can never leave this app holding a poisoned socket. Every function
/// here blocks; nothing in this file may be called from the main thread.
/// `DaemonClient` is the only caller and it always dispatches off-main.
enum UnixSocket {

    /// Sends one length-prefixed frame and returns the length-prefixed reply.
    static func roundTrip(
        path: String,
        request: Data,
        timeout: TimeInterval
    ) throws -> Data {
        // Distinguish "never installed" from "installed but stopped" before we
        // touch the socket, because the two have different fixes and connect(2)
        // reports both as a bare errno.
        if !FileManager.default.fileExists(atPath: path) {
            throw DaemonError.notInstalled(path: path)
        }

        let fd = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else {
            throw DaemonError.io("socket() failed: \(errnoText())")
        }
        defer { Darwin.close(fd) }

        // Without SO_NOSIGPIPE a daemon that closes mid-write kills the app
        // with SIGPIPE instead of returning EPIPE.
        var on: Int32 = 1
        _ = withUnsafePointer(to: &on) {
            setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, $0, socklen_t(MemoryLayout<Int32>.size))
        }

        var deadline = timeval(
            tv_sec: Int(timeout),
            tv_usec: Int32((timeout - timeout.rounded(.down)) * 1_000_000)
        )
        _ = withUnsafePointer(to: &deadline) {
            setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, $0, socklen_t(MemoryLayout<timeval>.size))
        }
        _ = withUnsafePointer(to: &deadline) {
            setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, $0, socklen_t(MemoryLayout<timeval>.size))
        }

        try connect(fd: fd, path: path)
        try writeAll(fd: fd, data: request)
        return try readFrame(fd: fd)
    }

    // MARK: - Connect

    private static func connect(fd: Int32, path: String) throws {
        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)

        let pathBytes = path.utf8CString
        let capacity = MemoryLayout.size(ofValue: addr.sun_path)
        guard pathBytes.count <= capacity else {
            throw DaemonError.io(
                "socket path is \(pathBytes.count) bytes, over the \(capacity) byte sun_path limit"
            )
        }
        withUnsafeMutablePointer(to: &addr.sun_path) { tuple in
            tuple.withMemoryRebound(to: CChar.self, capacity: capacity) { dst in
                pathBytes.withUnsafeBufferPointer { src in
                    guard let base = src.baseAddress else { return }
                    dst.update(from: base, count: src.count)
                }
            }
        }

        let length = socklen_t(MemoryLayout<sockaddr_un>.size)
        var result: Int32 = -1
        repeat {
            result = withUnsafePointer(to: &addr) { unPtr in
                unPtr.withMemoryRebound(to: sockaddr.self, capacity: 1) { sockPtr in
                    Darwin.connect(fd, sockPtr, length)
                }
            }
        } while result != 0 && errno == EINTR

        guard result != 0 else { return }

        switch errno {
        case ENOENT:
            // Raced with an uninstall, or the file vanished between the
            // existence check and here.
            throw DaemonError.notInstalled(path: path)
        case ECONNREFUSED:
            // A socket node with no listener: the daemon died without cleaning
            // up. Distinct from "not installed" and worth saying so.
            throw DaemonError.notRunning(path: path)
        case EACCES, EPERM:
            throw DaemonError.permissionDenied(path: path)
        case EAGAIN, ETIMEDOUT:
            throw DaemonError.timedOut
        default:
            throw DaemonError.io("connect() failed: \(errnoText())")
        }
    }

    // MARK: - Framing

    /// 4-byte big-endian length, then that many bytes of JSON.
    static func frame(_ body: Data) throws -> Data {
        guard body.count <= WGProtocol.maxFrameBytes else {
            throw DaemonError.io(
                "request is \(body.count) bytes, over the \(WGProtocol.maxFrameBytes) byte limit"
            )
        }
        var out = Data(capacity: body.count + 4)
        let length = UInt32(body.count).bigEndian
        withUnsafeBytes(of: length) { out.append(contentsOf: $0) }
        out.append(body)
        return out
    }

    private static func readFrame(fd: Int32) throws -> Data {
        let header = try readExactly(fd: fd, count: 4)
        let length = header.withUnsafeBytes { raw -> UInt32 in
            UInt32(bigEndian: raw.loadUnaligned(as: UInt32.self))
        }
        guard length <= UInt32(WGProtocol.maxFrameBytes) else {
            throw DaemonError.io(
                "the background service announced a \(length) byte reply, over the "
                    + "\(WGProtocol.maxFrameBytes) byte limit"
            )
        }
        if length == 0 { return Data() }
        return try readExactly(fd: fd, count: Int(length))
    }

    // MARK: - Raw I/O

    /// Reads exactly `count` bytes, looping over short reads. A stream socket
    /// is free to hand back one byte at a time; treating the first `read` as
    /// the whole message is the classic way to get intermittent JSON errors.
    private static func readExactly(fd: Int32, count: Int) throws -> Data {
        var buffer = [UInt8](repeating: 0, count: count)
        var filled = 0
        while filled < count {
            let received: Int = buffer.withUnsafeMutableBytes { raw in
                guard let base = raw.baseAddress else { return -1 }
                return Darwin.read(fd, base.advanced(by: filled), count - filled)
            }
            if received > 0 {
                filled += received
                continue
            }
            if received == 0 {
                throw DaemonError.io(
                    filled == 0
                        ? "the background service closed the connection without replying"
                        : "the background service closed the connection after \(filled) of \(count) bytes"
                )
            }
            switch errno {
            case EINTR:
                continue
            case EAGAIN, EWOULDBLOCK, ETIMEDOUT:
                throw DaemonError.timedOut
            default:
                throw DaemonError.io("read failed: \(errnoText())")
            }
        }
        return Data(buffer)
    }

    /// Writes the whole buffer, looping over partial writes.
    private static func writeAll(fd: Int32, data: Data) throws {
        var sent = 0
        let total = data.count
        while sent < total {
            let wrote: Int = data.withUnsafeBytes { raw in
                guard let base = raw.baseAddress else { return -1 }
                return Darwin.write(fd, base.advanced(by: sent), total - sent)
            }
            if wrote > 0 {
                sent += wrote
                continue
            }
            switch errno {
            case EINTR:
                continue
            case EAGAIN, EWOULDBLOCK, ETIMEDOUT:
                throw DaemonError.timedOut
            case EPIPE, ECONNRESET:
                throw DaemonError.io("the background service closed the connection while receiving the request")
            default:
                throw DaemonError.io("write failed: \(errnoText())")
            }
        }
    }

    private static func errnoText() -> String {
        String(cString: strerror(errno))
    }
}
