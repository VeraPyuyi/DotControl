package codexbridge

import (
	"encoding/json"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"strings"
	"testing"
)

func TestAbsentExperimentFieldDoesNotChangeLegacySerialization(t *testing.T) {
	data, _ := json.Marshal(model.CodexSubmit{DeviceID: "d", RequestID: "r", Prompt: "p"})
	if strings.Contains(string(data), "experiment") {
		t.Fatal(string(data))
	}
	want := `{"device_id":"d","request_id":"r","prompt":"p","allowed_apps":null,"working_dirs":null}`
	if string(data) != want {
		t.Fatal(string(data))
	}
}
