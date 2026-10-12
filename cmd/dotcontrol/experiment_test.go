package main

import (
	"context"
	"testing"
)

func TestExperimentCLIRequiresExplicitOperationAndSavedProfile(t *testing.T) {
	for _, args := range [][]string{nil, {"deploy"}, {"submit"}, {"status", "--profile", "fixture", "--experiment-id", "../unsafe"}, {"stop", "--profile", "fixture", "--experiment-id", "safe", "extra"}} {
		if e := experimentCLI(context.Background(), t.TempDir(), args); e == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
