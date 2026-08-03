//go:build windows

package overlay

import "os"

// currentUmask: Windows has no umask. 0 keeps the exec-bit restoration
// math (perm | 0o111 &^ umask) a no-op-safe identity — exec bits do not
// exist on NTFS anyway.
func currentUmask() os.FileMode { return 0 }
