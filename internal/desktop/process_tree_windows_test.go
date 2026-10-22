//go:build windows

package desktop

import (
	"context"
	"os"
	"testing"
)

func TestWindowsProcessSnapshotSupportsParentAgeCheck(t *testing.T) {
	processes, err := listProgramProcesses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, process := range processes {
		if process.PID != os.Getpid() {
			continue
		}
		// A child dating from the Windows epoch cannot belong to this test
		// process. Exercise the actual snapshot metadata used by the waiter.
		tree := &ProcessTree{ids: map[int]bool{process.PID: true}, created: map[int]uint64{}}
		tree.remainingProcesses([]programProcess{
			{PID: -1, ParentPID: process.PID, Created: 1},
			process,
		})
		if ids := tree.PIDs(); len(ids) != 1 || ids[0] != process.PID {
			t.Fatalf("Windows snapshot could not reject an older child: %v", ids)
		}
		return
	}
	t.Fatal("test process absent from Windows snapshot")
}
