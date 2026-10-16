//go:build !linux

package dataplane

import "syscall"

// markControl is a no-op off Linux, which has no SO_MARK; it lets the
// resolver run in tests.
func markControl(uint32) func(network, address string, c syscall.RawConn) error {
	return nil
}
