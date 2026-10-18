package desktop

import (
	"context"
	"errors"
	"github.com/gofrs/flock"
	"path/filepath"
	"testing"
	"time"
)

func TestSupervisorNormalQuitAndBoundedFailureRestart(t *testing.T) {
	for _, failures := range []int{0, 1, 8} {
		t.Run(string(rune('0'+failures)), func(t *testing.T) {
			calls := 0
			err := superviseChild(context.Background(), t.TempDir(), func() error {
				calls++
				if calls <= failures {
					return errors.New("crash")
				}
				return nil
			}, time.Millisecond)
			want := failures + 1
			if want > 4 {
				want = 4
			}
			if calls != want {
				t.Fatalf("starts=%d want %d", calls, want)
			}
			if (err != nil) != (failures > 3) {
				t.Fatal(err)
			}
		})
	}
}
func TestSupervisorSuspensionInterruptsBackoffWithoutRelaunch(t *testing.T) {
	state := t.TempDir()
	calls := 0
	start := time.Now()
	e := superviseChild(context.Background(), state, func() error {
		calls++
		if e := suspendRuntime(state); e != nil {
			t.Fatal(e)
		}
		return errors.New("crash")
	}, time.Minute)
	if e != nil || calls != 1 || time.Since(start) > 2*time.Second {
		t.Fatalf("calls=%d elapsed=%v err=%v", calls, time.Since(start), e)
	}
}
func TestSupervisorDuplicateIsCleanNoop(t *testing.T) {
	state := t.TempDir()
	lock := flock.New(filepath.Join(state, "background.lock"))
	ok, e := lock.TryLock()
	if e != nil || !ok {
		t.Fatal(e)
	}
	defer lock.Close()
	if e = superviseChild(context.Background(), state, func() error { t.Fatal("duplicate child"); return nil }, time.Millisecond); e != nil {
		t.Fatal(e)
	}
}
