package connections

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCommandHelperProcess(t *testing.T) {
	if os.Getenv("DOTCONTROL_CONNECTION_TEST_HELPER") != "1" {
		return
	}
	if os.Args[len(os.Args)-1] == "wait" {
		time.Sleep(time.Hour)
		return
	}
	if os.Args[len(os.Args)-1] == "metadata" {
		for pid := 1; pid <= 2000; pid++ {
			fmt.Fprintf(os.Stdout, "%d 0 /usr/bin/example-process-with-a-long-name\n", pid)
		}
		fmt.Fprint(os.Stdout, "3000 0 /Applications/Codex.app/Contents/MacOS/Codex\n3001 3000 /Applications/Codex.app/Contents/Resources/codex\n")
		os.Exit(0)
	}
	if os.Args[len(os.Args)-1] == "metadata-overflow" {
		fmt.Fprint(os.Stdout, strings.Repeat("1 0 /usr/bin/example-process\n", 50000))
		os.Exit(0)
	}
	fmt.Fprint(os.Stdout, strings.Repeat("o", 32768))
	fmt.Fprint(os.Stderr, strings.Repeat("e", 32768))
	os.Exit(7)
}

func TestProcessMetadataCaptureRetainsLateDesktopAncestry(t *testing.T) {
	t.Setenv("DOTCONTROL_CONNECTION_TEST_HELPER", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	result, err := runCommandWithStdoutLimit(context.Background(), exe, []string{"-test.run=^TestCommandHelperProcess$", "--", "metadata"}, processMetadataLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Stdout) <= 16384 {
		t.Fatal("fixture did not exceed ordinary capture limit")
	}
	processes, err := parseProcessMetadata(result)
	if err != nil {
		t.Fatal(err)
	}
	candidates := runningDesktopCandidates(processes)
	if len(candidates) != 1 || candidates[0].Source != SourceRunningDesktop {
		t.Fatal("late ancestry lost")
	}
}

func TestProcessMetadataCaptureRejectsTruncationWithoutParsingPartialList(t *testing.T) {
	t.Setenv("DOTCONTROL_CONNECTION_TEST_HELPER", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	result, err := runCommandWithStdoutLimit(context.Background(), exe, []string{"-test.run=^TestCommandHelperProcess$", "--", "metadata-overflow"}, processMetadataLimit)
	if err != nil {
		t.Fatal(err)
	}
	if !result.StdoutTruncated || len(result.Stdout) != processMetadataLimit {
		t.Fatal("metadata cap not enforced")
	}
	processes, err := parseProcessMetadata(result)
	if err == nil || len(processes) != 0 || err.Error() != "process_metadata_truncated" {
		t.Fatal("partial process scan accepted", err)
	}
}

func TestCommandRunnerCapsOutputAndPreservesExit(t *testing.T) {
	t.Setenv("DOTCONTROL_CONNECTION_TEST_HELPER", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	result, err := runCommand(context.Background(), exe, []string{"-test.run=^TestCommandHelperProcess$", "--", "output"})
	if err == nil || result.ExitCode != 7 || len(result.Stdout) != 16384 || len(result.Stderr) != 16384 {
		t.Fatalf("unexpected capture: exit=%d stdout=%d stderr=%d err=%v", result.ExitCode, len(result.Stdout), len(result.Stderr), err)
	}
}

func TestCommandRunnerStopsAtDeadline(t *testing.T) {
	t.Setenv("DOTCONTROL_CONNECTION_TEST_HELPER", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = runCommand(ctx, exe, []string{"-test.run=^TestCommandHelperProcess$", "--", "wait"})
	if err != context.DeadlineExceeded || time.Since(started) > 3*time.Second {
		t.Fatalf("unbounded command: %v", err)
	}
}

// Opt-in, read-only local acceptance. It invokes only a discovered --version.
func TestLocalCodexDiscovery(t *testing.T) {
	if os.Getenv("DOTCONTROL_TEST_CODEX_DISCOVERY") != "1" {
		t.Skip("local discovery acceptance not requested")
	}
	result, err := DiscoverCodex(context.Background(), "", DiscoveryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Available || result.Executable == "" {
		t.Fatal("no usable local executable")
	}
	t.Logf("source=%s version=%s code=%s", result.Source, result.Version, result.Code)
}
