package desktop

import (
	"reflect"
	"testing"
)

func TestProcessTreeRejectsChildOlderThanReusedParentPID(t *testing.T) {
	// Windows leaves a child's original parent PID in the process table after
	// that parent exits. A new console host can reuse it much later.
	tree := &ProcessTree{ids: map[int]bool{17540: true}, created: map[int]uint64{}}
	if !tree.remainingProcesses([]programProcess{
		{PID: 31488, ParentPID: 19152, Created: 100},
		{PID: 19152, ParentPID: 17540, Created: 300},
		{PID: 17540, ParentPID: 1, Created: 200},
	}) {
		t.Fatal("live agent was not retained")
	}
	if got := tree.PIDs(); !reflect.DeepEqual(got, []int{17540, 19152}) {
		t.Fatalf("adopted a process older than its purported parent: %v", got)
	}
	// The creation evidence must survive the parent's exit, or the next poll
	// would adopt the same unrelated process and wait until the stop deadline.
	if tree.remainingProcesses([]programProcess{{PID: 31488, ParentPID: 19152, Created: 100}}) {
		t.Fatal("unrelated old process blocked exit after the owned tree exited")
	}
}

func TestProcessTreePersistedIDsKeepUncertainAncestry(t *testing.T) {
	// Recovery records contain only PIDs, not the original creation times. The
	// current PID occupant may be newer than a genuine, now-orphaned child.
	tree := &ProcessTree{ids: map[int]bool{10: true}}
	tree.remainingProcesses([]programProcess{
		{PID: 10, Created: 300},
		{PID: 20, ParentPID: 10, Created: 100},
	})
	if !tree.remainingProcesses([]programProcess{{PID: 20, ParentPID: 10, Created: 100}}) {
		t.Fatal("recovery discarded uncertain descendant evidence")
	}
}

func TestProcessTreeKeepsDescendantsAndUnknownCreationEvidence(t *testing.T) {
	for _, tc := range []struct {
		name          string
		parent, child uint64
	}{
		{"newer child", 200, 300},
		{"same timestamp", 200, 200},
		{"unknown child", 200, 0},
		{"unknown parent", 0, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree := &ProcessTree{ids: map[int]bool{10: true}, created: map[int]uint64{}}
			tree.remainingProcesses([]programProcess{
				{PID: 30, ParentPID: 20, Created: tc.child},
				{PID: 20, ParentPID: 10, Created: tc.parent},
				{PID: 10},
			})
			if got := tree.PIDs(); !reflect.DeepEqual(got, []int{10, 20, 30}) {
				t.Fatalf("lost conservative descendant evidence: %v", got)
			}
			if !tree.remainingProcesses([]programProcess{{PID: 30, ParentPID: 20, Created: tc.child}}) {
				t.Fatal("orphaned descendant no longer blocks exit")
			}
			if tree.remainingProcesses(nil) {
				t.Fatal("empty process table still blocks exit")
			}
			// Persisted PID evidence deliberately remains conservative under reuse.
			if !tree.remainingProcesses([]programProcess{{PID: 30, Created: 900}}) {
				t.Fatal("occupied recorded PID no longer blocks exit")
			}
		})
	}
}
