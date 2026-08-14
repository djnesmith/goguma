package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The installer's most dangerous moment is copying a binary to a root-owned
// path and loading it as a LaunchDaemon. Everything else it prints about that
// binary is a claim; this is the one check made against the bytes, so it is
// worth proving it actually fails when the bytes change.
func TestTamperedBinaryFailsVerification(t *testing.T) {
	if _, err := exec.LookPath("codesign"); err != nil {
		t.Skip("codesign unavailable")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "victim")

	// Any Mach-O will do; /bin/echo is signed by Apple and always present.
	src, err := os.ReadFile("/bin/echo")
	if err != nil {
		t.Skip("no /bin/echo to copy")
	}
	if err := os.WriteFile(bin, src, 0o755); err != nil {
		t.Fatal(err)
	}

	if sig, err := checkSignature(bin); err != nil || !sig.Valid {
		t.Skipf("copy did not verify cleanly (%v); nothing to tamper with", err)
	}

	// One byte, in the middle, exactly what a modified download looks like.
	b, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)/2] ^= 0xFF
	if err := os.WriteFile(bin, b, 0o755); err != nil {
		t.Fatal(err)
	}

	sig, err := checkSignature(bin)
	if err == nil {
		t.Error("a modified binary passed verification")
	}
	if sig.Valid {
		t.Error("a modified binary reported a valid signature")
	}
}

// "Could not check" and "checked, and it is fine" must never look the same, so
// an unsigned binary reports nothing rather than reporting success.
func TestUnsignedBinarySaysNothingRatherThanClaimingSafety(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "plain")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sig, _ := checkSignature(bin)
	if sig.Describe() != "" && !sig.AdHoc {
		t.Errorf("unsigned binary described itself as %q", sig.Describe())
	}
}
