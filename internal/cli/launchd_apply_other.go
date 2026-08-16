//go:build !darwin

package cli

import "fmt"

func applyLaunchdWrap(_ *Context, _, _, _, _, _ string) error {
	return fmt.Errorf("launchd only exists on macOS")
}

func needsRootToWrap(_ string) bool { return false }
