//go:build !windows

package probe

import "syscall"

// pidAlive is the signal-0 liveness check; EPERM means alive but not ours.
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
