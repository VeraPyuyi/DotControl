package codexbridge

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf16"
)

// PowerShellCommand renders the invocation used in task instructions.
// An explicit pipeline is required for PowerShell to wait for a Windows GUI
// executable. Converting error records to strings preserves helper diagnostics
// without PowerShell's invocation/path decoration, and exit propagates failure.
// Encode the complete argument array as UTF-8 JSON: PowerShell recognizes smart
// quotes as syntax even inside ASCII-quoted literals, so apostrophe escaping is
// insufficient. Decoded strings are passed as values, never evaluated as code.
func PowerShellCommand(arguments []string) string {
	if len(arguments) == 0 {
		return "throw 'invalid_helper_command'"
	}
	raw, _ := json.Marshal(arguments)
	encoded := base64.StdEncoding.EncodeToString(raw)
	// Show the exact reversible JSON argv to a human reviewing this command.
	// ASCII escapes prevent every Unicode quote/line separator from becoming
	// shell syntax. JSON has already escaped literal CR/LF and other controls.
	var display strings.Builder
	for _, r := range string(raw) {
		switch {
		case r <= 127:
			display.WriteRune(r)
		case r <= 0xffff:
			fmt.Fprintf(&display, `\u%04x`, r)
		default:
			high, low := utf16.EncodeRune(r)
			fmt.Fprintf(&display, `\u%04x\u%04x`, high, low)
		}
	}
	return "# DotControl helper argv: " + display.String() + "\n" + "$ErrorActionPreference = 'Continue'; $PSNativeCommandUseErrorActionPreference = $false; [string[]]$dcCommand = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('" + encoded + "')) | ConvertFrom-Json; $dcExecutable = $dcCommand[0]; $dcArgs = @($dcCommand | Select-Object -Skip 1); $dcOutput = @(& $dcExecutable @dcArgs 2>&1 | ForEach-Object { $_.ToString() }); $dcExit = $LASTEXITCODE; foreach ($dcLine in $dcOutput) { [Console]::Out.WriteLine($dcLine) }; if ($null -eq $dcExit) { [Console]::Error.WriteLine('helper_exit_unconfirmed'); exit 1 }; exit $dcExit"
}
