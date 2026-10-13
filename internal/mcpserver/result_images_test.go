package mcpserver

import (
	"encoding/base64"
	"encoding/json"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"strings"
	"testing"
)

func TestAllJobReturningMethodsExtractImages(t *testing.T) {
	data := base64.StdEncoding.EncodeToString([]byte("test image"))
	for _, method := range []string{"codex_submit", "codex_continue", "codex_cancel", "codex_job"} {
		res, out, err := formatResult(method, model.CodexJob{Runtime: &model.CodexRuntimeInfo{Images: []model.Image{{MIMEType: "image/png", Data: data}}}})
		if err != nil || res == nil || len(res.Content) != 2 {
			t.Fatalf("%s: %v", method, err)
		}
		encoded, _ := json.Marshal(out)
		if strings.Contains(string(encoded), data) {
			t.Fatalf("%s base64 duplicated", method)
		}
	}
}

func TestCodexGIFDoesNotDiscardTextResult(t *testing.T) {
	data := base64.StdEncoding.EncodeToString([]byte("GIF89a transport fixture"))
	for _, method := range []string{"codex_submit", "codex_continue", "codex_job", "codex_cancel"} {
		t.Run(method, func(t *testing.T) {
			res, out, err := formatResult(method, model.CodexJob{Runtime: &model.CodexRuntimeInfo{Text: "result evidence", Images: []model.Image{{MIMEType: "image/gif", Data: data}}}})
			if err != nil || res == nil || len(res.Content) != 2 {
				t.Fatalf("GIF made job unreadable: %v", err)
			}
			image, ok := res.Content[1].(*mcp.ImageContent)
			if !ok || image.MIMEType != "image/gif" {
				t.Fatal("GIF not returned as native image")
			}
			job, ok := out.(model.CodexJob)
			if !ok || job.Runtime.Text != "result evidence" || len(job.Runtime.Images) != 0 {
				t.Fatal("text evidence lost or image duplicated")
			}
		})
	}
}
