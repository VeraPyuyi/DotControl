package codexbridge

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestPowerShellInvocationQuotesArgumentsAndWaitsForExit(t *testing.T) {
	arguments := []string{`C:\Program Files\dotcontrol 助手 ‘智能’.exe`, "--state", `C:\state with 'quote' ‘智能’ $x;`, "terminal-check-helper", "--nonce", "test\nline\r\u0085\u2028`#>"}
	command := PowerShellCommand(arguments)
	for _, want := range []string{"[Text.Encoding]::UTF8.GetString", "ConvertFrom-Json", "2>&1 |", "$LASTEXITCODE", "exit $dcExit"} {
		if !strings.Contains(command, want) {
			t.Fatalf("missing required PowerShell behavior %q: %s", want, command)
		}
	}
	encoded := regexp.MustCompile(`FromBase64String\('([A-Za-z0-9+/=]+)'\)`).FindStringSubmatch(command)
	if len(encoded) != 2 {
		t.Fatal("missing ASCII-only argument encoding")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded[1])
	if err != nil {
		t.Fatal(err)
	}
	var decoded []string
	if json.Unmarshal(raw, &decoded) != nil || !reflect.DeepEqual(decoded, arguments) {
		t.Fatalf("arguments changed: %q", decoded)
	}
	if strings.ContainsAny(command, "‘’智能") {
		t.Fatal("user characters embedded in shell syntax")
	}
	comment, _, ok := strings.Cut(command, "\n")
	if !ok || strings.Count(command, "\n") != 1 || !strings.HasPrefix(comment, "# DotControl helper argv: ") {
		t.Fatal("missing safe reviewable argv comment")
	}
	var visible []string
	if json.Unmarshal([]byte(strings.TrimPrefix(comment, "# DotControl helper argv: ")), &visible) != nil || !reflect.DeepEqual(visible, arguments) {
		t.Fatal("review comment changed or hid arguments")
	}
	for _, r := range command {
		if r > 127 {
			t.Fatal("non-ASCII line separator or quote entered command")
		}
	}
}
