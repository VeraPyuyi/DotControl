//go:build windows

package activity

import (
	"sync/atomic"
	"testing"
)

func newInputTracker() (*systemInputTracker, *atomic.Int64, *atomic.Uint64) {
	last := &atomic.Int64{}
	generation := &atomic.Uint64{}
	return &systemInputTracker{lastActivity: last, generation: generation}, last, generation
}

func TestSystemInputChangeCountsWithoutRawMouseOrKeyboard(t *testing.T) {
	tracker, last, generation := newInputTracker()
	if !tracker.merge(100000, 90000, 10000, true) {
		t.Fatal("baseline unavailable")
	}
	if !tracker.merge(100020, 90020, 90010, true) {
		t.Fatal("input sample unavailable")
	}
	if last.Load() != 100020 || generation.Load() != 1 {
		t.Fatalf("unclassified input missed: last=%d generation=%d", last.Load(), generation.Load())
	}
}

func TestSystemInputClockNeverRequiresIncreasingEventTicks(t *testing.T) {
	for _, next := range []uint32{80000, 10} {
		tracker, last, generation := newInputTracker()
		tracker.merge(100000, 90000, 85000, true)
		tracker.merge(100020, 90020, next, true)
		if last.Load() != 100020 || generation.Load() != 1 {
			t.Fatalf("changed nonmonotonic input missed: tick=%d last=%d generation=%d", next, last.Load(), generation.Load())
		}
	}
}

func TestUnchangedSystemInputPreservesRawActivity(t *testing.T) {
	tracker, last, generation := newInputTracker()
	tracker.merge(100000, 90000, 10000, true)
	last.Store(100010)
	generation.Add(1)
	tracker.merge(100020, 90020, 10000, true)
	if last.Load() != 100010 || generation.Load() != 1 {
		t.Fatalf("unchanged system timestamp overwrote raw activity: last=%d generation=%d", last.Load(), generation.Load())
	}
}

func TestMissingSystemInputFailsClosedUntilRecovery(t *testing.T) {
	tracker, last, generation := newInputTracker()
	tracker.merge(100000, 90000, 10000, true)
	if tracker.merge(100020, 0, 0, false) || last.Load() != 100020 || generation.Load() != 1 {
		t.Fatalf("failed poll admitted idle: last=%d generation=%d", last.Load(), generation.Load())
	}
	if !tracker.merge(100040, 90040, 10000, true) || last.Load() != 100020 {
		t.Fatalf("recovery erased uncertain activity: last=%d", last.Load())
	}
}

func TestSystemInputTickWrapAndWallClockChangeRemainConservative(t *testing.T) {
	tracker, last, _ := newInputTracker()
	if !tracker.merge(100000, 32, 0xfffffff0, true) || last.Load() != 99952 {
		t.Fatalf("wrap calculation=%d", last.Load())
	}
	// System uptime remains monotonic even when wall time advances abruptly.
	tracker.merge(200000, 52, 0xfffffff0, true)
	if last.Load() != 199932 {
		t.Fatalf("wall clock jump exaggerated idle: %d", last.Load())
	}
}

func TestImplausibleSystemTickFailsClosed(t *testing.T) {
	tracker, last, generation := newInputTracker()
	if tracker.merge(100000, 90000, 90001, true) || last.Load() != 100000 || generation.Load() != 1 {
		t.Fatalf("future input timestamp admitted idle: last=%d generation=%d", last.Load(), generation.Load())
	}
}
