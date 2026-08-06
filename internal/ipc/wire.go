// Package ipc carries requests between the CLI, the GUI, the daemon, and the
// privileged helper.
//
// The transport is length-prefixed JSON over a Unix domain socket. Length
// prefixing rather than newline delimiting because payloads (job lists,
// history) are large and structured; a stream parser that has to scan for
// delimiters inside JSON strings is a class of bug worth designing out.
package ipc

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// maxFrame bounds a single message. History responses are the largest
// realistic payload and are far under this; the cap exists so a corrupt or
// hostile length prefix cannot make the peer allocate unbounded memory.
const maxFrame = 8 << 20 // 8 MiB

// WriteFrame writes a length-prefixed JSON message.
func WriteFrame(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding message: %w", err)
	}
	if len(b) > maxFrame {
		return fmt.Errorf("message is %d bytes, over the %d byte limit", len(b), maxFrame)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// ReadFrame reads one length-prefixed JSON message into v.
func ReadFrame(r io.Reader, v any) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > maxFrame {
		return fmt.Errorf("peer announced a %d byte message, over the %d byte limit", n, maxFrame)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	return json.Unmarshal(buf, v)
}
