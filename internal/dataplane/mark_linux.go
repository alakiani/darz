package dataplane

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// markControl sets SO_MARK on a socket before it connects, so policy
// routing sends it where the mark says.
func markControl(mark uint32) func(network, address string, c syscall.RawConn) error {
	return func(_, _ string, c syscall.RawConn) error {
		var serr error
		err := c.Control(func(fd uintptr) {
			serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, int(mark))
		})
		if err != nil {
			return err
		}
		return serr
	}
}
