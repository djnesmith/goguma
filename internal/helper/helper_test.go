package helper

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/ipc"
)

func testService() *Service {
	return New("test", slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestHelperRejectsUnknownOperations(t *testing.T) {
	// The privileged surface is deliberately tiny. Anything outside it must be
	// refused rather than silently ignored.
	s := testService()
	if _, err := s.Handle(context.Background(), ipc.Op("helper.rm_rf"), nil); err == nil {
		t.Fatal("an unknown operation was accepted")
	}
}

func TestHelperOperationSurfaceIsMinimal(t *testing.T) {
	// A guard on scope: if someone adds a mutating operation here, this fails
	// and forces the addition to be deliberate. The helper runs as root.
	s := testService()
	allowed := map[ipc.Op]bool{
		ipc.OpPing: true, ipc.OpHelperStatus: true,
		ipc.OpHelperSetSleepBlocked: true,
		ipc.OpHelperScheduleWake:    true, ipc.OpHelperCancelWake: true,
	}
	for _, op := range []ipc.Op{
		ipc.OpJobsAdd, ipc.OpJobsRemove, ipc.OpConfigSet, ipc.OpSync,
		ipc.OpStatus, ipc.OpMarkStart, ipc.OpSleepNow,
	} {
		if allowed[op] {
			continue
		}
		if _, err := s.Handle(context.Background(), op, nil); err == nil {
			t.Errorf("the privileged helper accepted %q; its surface must stay minimal", op)
		}
	}
}

func TestSetSleepBlockedRejectsMalformedPayloads(t *testing.T) {
	s := testService()
	for _, bad := range []string{`{`, `[]`, `"string"`, `{"blocked":"yes"}`} {
		if _, err := s.Handle(context.Background(),
			ipc.OpHelperSetSleepBlocked, json.RawMessage(bad)); err == nil {
			t.Errorf("malformed payload %q was accepted", bad)
		}
	}
}

func TestScheduleWakeRejectsMalformedPayloads(t *testing.T) {
	s := testService()
	for _, bad := range []string{`{`, `[]`, `{"at":"not-a-time"}`} {
		if _, err := s.Handle(context.Background(),
			ipc.OpHelperScheduleWake, json.RawMessage(bad)); err == nil {
			t.Errorf("malformed payload %q was accepted", bad)
		}
	}
}

// TestDeadManOnlyFiresWhenBlockedAndAbandoned covers the safety net that stops
// a stranded global sleep block.
//
// disablesleep is NOT cleared when the process that set it exits, so if the
// daemon is SIGKILLed at logout while blocked, nothing else would ever clear
// it and the machine could not sleep until someone noticed.
func TestDeadManOnlyFiresWhenBlockedAndAbandoned(t *testing.T) {
	t.Run("not blocked: nothing to do", func(t *testing.T) {
		s := testService()
		s.blocked = false
		s.lastContact = time.Now().Add(-time.Hour)
		s.checkDeadMan()
		if s.blocked {
			t.Error("state changed while not blocked")
		}
	})

	t.Run("blocked but a daemon is connected", func(t *testing.T) {
		s := testService()
		s.blocked = true
		s.connections = 1
		s.lastContact = time.Now().Add(-time.Hour)
		s.checkDeadMan()
		if !s.blocked {
			t.Error("released while a daemon was still connected")
		}
	})

	t.Run("blocked and recently in touch", func(t *testing.T) {
		s := testService()
		s.blocked = true
		s.lastContact = time.Now()
		s.checkDeadMan()
		if !s.blocked {
			t.Error("released while the daemon was still checking in")
		}
	})
}

func TestConnectionCountingNeverGoesNegative(t *testing.T) {
	// The count gates the dead-man switch. If it could go negative, a later
	// genuine disconnect would leave it above zero and the switch would never
	// fire; the block would stay stranded.
	s := testService()
	s.ConnectionClosed()
	s.ConnectionClosed()
	if s.connections < 0 {
		t.Fatalf("connections = %d, want it floored at zero", s.connections)
	}
	s.ConnectionOpened()
	s.ConnectionClosed()
	if s.connections != 0 {
		t.Errorf("connections = %d after a balanced pair, want 0", s.connections)
	}
}

func TestHandleRefreshesLastContact(t *testing.T) {
	// Every request is a heartbeat. If it were not, a busy daemon could be
	// timed out by the dead-man switch and have its block cleared mid-job.
	s := testService()
	s.lastContact = time.Now().Add(-time.Hour)
	if _, err := s.Handle(context.Background(), ipc.OpPing, nil); err != nil {
		t.Fatal(err)
	}
	if time.Since(s.lastContact) > time.Minute {
		t.Error("a request did not count as contact")
	}
}

func TestPingReportsTheVersion(t *testing.T) {
	s := New("1.2.3", slog.New(slog.NewTextHandler(io.Discard, nil)))
	out, err := s.Handle(context.Background(), ipc.OpPing, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := out.(map[string]string)
	if !ok || m["version"] != "1.2.3" {
		t.Errorf("ping returned %v, want the version", out)
	}
}

func TestMechanismIsNamed(t *testing.T) {
	// `status` reports what is actually holding sleep off rather than a generic
	// success, so the mechanism must be stated.
	if strings.TrimSpace(Mechanism) == "" {
		t.Error("the sleep-blocking mechanism should be named for status output")
	}
}
