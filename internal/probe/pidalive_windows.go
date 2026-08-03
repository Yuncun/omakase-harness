//go:build windows

package probe

import "os"

// pidAlive: Windows has no signal 0; os.FindProcess there actually opens a
// process handle (unlike Unix, where it always succeeds), so the open
// itself is the liveness check. Heartbeat pids are the user's own gate
// processes, so an access-denied open (another user's pid recycled into the
// slot) correctly reads as not-ours-not-alive.
func pidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	p.Release()
	return true
}
