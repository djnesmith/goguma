package install

import "github.com/junnam586/goguma/internal/paths"

// unitPath is the user-level service definition for this platform.
func unitPath(l paths.Layout) string { return l.DaemonUnitFile() }
