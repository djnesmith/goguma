package detect

import "testing"

// `ps -o args=` prints argv newlines raw, so a multi-line command continues
// on lines of its own. Dropping those lines truncated the command at the
// newline (hiding the tokens a pattern needs) and let a digit-leading
// continuation parse as a phantom process.
func TestParsePSJoinsArgvNewlines(t *testing.T) {
	out := "  101 python3 -c import time\n" +
		"time.sleep(10)\n" +
		"  202 rsync -a /src /dst\n"
	procs, err := parsePS(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) != 2 {
		t.Fatalf("parsed %d processes, want 2: %+v", len(procs), procs)
	}
	if procs[0].Command != "python3 -c import time time.sleep(10)" {
		t.Errorf("command truncated at the argv newline: %q", procs[0].Command)
	}
	if procs[1].PID != 202 {
		t.Errorf("second process = %+v, want PID 202", procs[1])
	}
}
