package remoteaccess

import (
	"net"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// A held writer rechecks quickly at first, so a fast link is not paced by
	// the poll, then every unsentPollInterval.
	unsentFastPollInterval = 100 * time.Microsecond
	unsentFastPollFor      = 2 * time.Millisecond
	unsentPollInterval     = time.Millisecond
	// unsentMaxWait bounds one write's wait, so a carrier whose relay has
	// stopped reading falls through to the kernel's own blocking instead of
	// polling forever.
	unsentMaxWait = 2 * time.Second
)

// boundTCPUnsentBacklog wraps the connection so each write first waits for the
// unsent backlog to fall to relayCarrierUnsentLowat. macOS applies
// TCP_NOTSENT_LOWAT only to writability events, and Go writes before it polls,
// so the option alone does not hold a writer back here.
func boundTCPUnsentBacklog(conn *net.TCPConn) net.Conn {
	raw, err := conn.SyscallConn()
	if err != nil {
		return conn
	}
	return &unsentBoundConn{Conn: conn, raw: raw}
}

// unsentBoundConn embeds the connection as a net.Conn, not a *net.TCPConn, so
// no promoted method (ReadFrom) reaches the socket without passing Write.
type unsentBoundConn struct {
	net.Conn
	raw syscall.RawConn
	// writeDeadline is the caller's write deadline in Unix nanoseconds, zero
	// for none, so the wait ends when the write itself would time out.
	writeDeadline atomic.Int64
}

func (c *unsentBoundConn) SetDeadline(t time.Time) error {
	c.storeWriteDeadline(t)
	return c.Conn.SetDeadline(t)
}

func (c *unsentBoundConn) SetWriteDeadline(t time.Time) error {
	c.storeWriteDeadline(t)
	return c.Conn.SetWriteDeadline(t)
}

func (c *unsentBoundConn) storeWriteDeadline(t time.Time) {
	if t.IsZero() {
		c.writeDeadline.Store(0)
		return
	}
	c.writeDeadline.Store(t.UnixNano())
}

func (c *unsentBoundConn) Write(p []byte) (int, error) {
	for start := time.Now(); ; {
		unsent, ok := c.unsent()
		if !ok || unsent <= relayCarrierUnsentLowat {
			break
		}
		now := time.Now()
		waited := now.Sub(start)
		if waited >= unsentMaxWait {
			break
		}
		// Past the deadline the write below reports the timeout itself.
		if deadline := c.writeDeadline.Load(); deadline != 0 && now.UnixNano() >= deadline {
			break
		}
		if waited < unsentFastPollFor {
			time.Sleep(unsentFastPollInterval)
		} else {
			time.Sleep(unsentPollInterval)
		}
	}
	return c.Conn.Write(p)
}

// unsent estimates the bytes written but not yet sent: the send buffer's
// content less what the congestion and peer windows allow in flight. The
// kernel does not report the unsent count itself.
func (c *unsentBoundConn) unsent() (int64, bool) {
	var info *unix.TCPConnectionInfo
	var infoErr error
	if err := c.raw.Control(func(fd uintptr) {
		info, infoErr = unix.GetsockoptTCPConnectionInfo(int(fd), unix.IPPROTO_TCP, unix.TCP_CONNECTION_INFO) //nolint:gosec // G115: a file descriptor fits an int.
	}); err != nil || infoErr != nil {
		return 0, false
	}
	return int64(info.Snd_sbbytes) - int64(min(info.Snd_cwnd, info.Snd_wnd)), true
}
