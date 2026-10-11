package driver

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/model"
)

// This read-only opt-in probe must target the disposable fixture. It is not
// official Dot acceptance. Never configure a user's document as its target.
func TestLiveFixtureObservation(t *testing.T) {
	path := os.Getenv("DOTCONTROL_TEST_DRIVER")
	if path == "" {
		t.Skip("requires explicit disposable live fixture")
	}
	pid, e := strconv.Atoi(os.Getenv("DOTCONTROL_TEST_PID"))
	if e != nil {
		t.Fatal(e)
	}
	target := model.Target{PID: pid, WindowID: os.Getenv("DOTCONTROL_TEST_WINDOW")}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	d, e := New(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	args, _ := targetArgs(target)
	args["max_elements"] = 500
	args["max_image_dimension"] = 1600
	r, e := d.call(ctx, "get_window_state", args)
	if e != nil {
		t.Fatal(e)
	}
	if r.IsError {
		p, _ := payload(r)
		t.Fatalf("fixture snapshot driver error: %v; content: %v", p, r.Content)
	}
	for _, c := range r.Content {
		if txt, ok := c.(*mcp.TextContent); ok {
			t.Log(txt.Text)
		}
	}
	p, _ := payload(r)
	t.Logf("snapshot=%v capture=%v elements=%v", p["snapshot_id"], p["capture_id"], p["elements"])
	view, e := d.Observe(ctx, target)
	if e != nil {
		t.Fatal(e)
	}
	if len(view.Images) == 0 || len(view.Elements) == 0 {
		t.Fatal("fixture has no image or accessibility controls")
	}
	t.Logf("fixture observation: %d images, %d elements", len(view.Images), len(view.Elements))
}
