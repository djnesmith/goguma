package ipc

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"time"
)

// ErrNotRunning means nothing is listening on the socket.
//
// The text is deliberately neutral about *which* service is absent. This
// package is used for both the CLI-to-daemon and daemon-to-helper links, and a
// message naming the daemon was being logged verbatim when the daemon failed
// to reach the helper — telling the reader the daemon was down while the
// daemon itself wrote the line. Callers wrap this with the socket path, and
// the CLI translates it into user-facing advice.
var ErrNotRunning = errors.New("nothing is listening on the socket")

// Client is a short-lived connection to the daemon or helper.
type Client struct {
	conn net.Conn
}

// DefaultTimeout bounds a CLI round trip. The CLI is invoked from cron
// wrappers on a job's critical path, so it must fail fast rather than hang.
const DefaultTimeout = 5 * time.Second

// HelperTimeout bounds a daemon-to-helper round trip.
//
// Longer than DefaultTimeout, and deliberately longer than the helper's own
// pmsetTimeout (10s), because the caller's deadline must outlast the callee's.
// It did not: the daemon waited 5s for an operation the helper is allowed to
// spend 10s on, so any slow `pmset` — 1.3s cold on a healthy machine, and far
// worse when power management is busy — timed out on the daemon's side while
// the helper was still working. The helper then replied into a connection
// nobody was reading, the daemon declared it lost, reconnected, and did it all
// again. That is the reconnect loop, and no amount of reinstalling the helper
// could fix it, because both processes were behaving exactly as written.
const HelperTimeout = 15 * time.Second

// Dial connects to a socket.
func Dial(path string, timeout time.Duration) (*Client, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	conn, err := net.DialTimeout("unix", path, timeout)
	if err != nil {
		var opErr *net.OpError
		if errors.As(err, &opErr) || os.IsNotExist(err) {
			// Wrap with the path so a log line identifies which service is
			// missing without the reader having to guess.
			return nil, fmt.Errorf("%s: %w", path, ErrNotRunning)
		}
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	return &Client{conn: conn}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

// Call sends one request and decodes the reply into out, which may be nil.
func (c *Client) Call(op Op, payload any, out any) error {
	req := Request{Protocol: ProtocolVersion, Op: op}
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encoding %s request: %w", op, err)
		}
		req.Payload = b
	}
	if err := WriteFrame(c.conn, req); err != nil {
		return fmt.Errorf("sending %s: %w", op, err)
	}

	var resp Response
	if err := ReadFrame(c.conn, &resp); err != nil {
		return fmt.Errorf("reading reply to %s: %w", op, err)
	}
	if !resp.OK {
		if resp.Error == "" {
			resp.Error = "request failed"
		}
		return errors.New(resp.Error)
	}
	if out != nil && len(resp.Payload) > 0 {
		if err := json.Unmarshal(resp.Payload, out); err != nil {
			return fmt.Errorf("decoding %s reply: %w", op, err)
		}
	}
	return nil
}

// Do is the one-shot convenience form: dial, call, close.
func Do(path string, op Op, payload any, out any) error {
	return DoTimeout(path, DefaultTimeout, op, payload, out)
}

// DoTimeout is Do with an explicit deadline, for callers whose work legitimately
// takes longer than a CLI round trip.
func DoTimeout(path string, timeout time.Duration, op Op, payload any, out any) error {
	c, err := Dial(path, timeout)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Call(op, payload, out)
}
