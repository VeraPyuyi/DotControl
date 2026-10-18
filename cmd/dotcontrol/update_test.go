package main

import (
	"strings"
	"testing"
)

func TestReleaseSelection(t *testing.T) {
	asset := func(os, arch string) string {
		return `[{"name":"DotControl-` + os + `-` + arch + `.zip","browser_download_url":"https://github.com/VeraPyuyi/DotControl/releases/download/v0.2.1/DotControl-` + os + `-` + arch + `.zip"}]`
	}
	release := func(tag string, pre, draft bool, assets string) string {
		assets = strings.ReplaceAll(assets, "/v0.2.1/", "/"+tag+"/")
		p, d := "false", "false"
		if pre {
			p = "true"
		}
		if draft {
			d = "true"
		}
		return `{"tag_name":"` + tag + `","prerelease":` + p + `,"draft":` + d + `,"assets":` + assets + `}`
	}
	a := asset("windows", "amd64")
	for _, tc := range []struct{ name, current, body, want string }{
		{"sorted", "0.2.0-alpha.7", "[" + release("v0.2.0-alpha.8", true, false, a) + "," + release("v0.2.0-alpha.10", true, false, a) + "]", "v0.2.0-alpha.10"},
		{"stable", "0.2.0", "[" + release("v0.3.0-alpha.1", true, false, a) + "," + release("v0.2.1", false, false, a) + "]", "v0.2.1"},
		{"no cross channel", "0.2.0-alpha.7", "[" + release("v0.2.1", false, false, a) + "]", ""},
		{"downgrade", "0.2.0-alpha.7", "[" + release("v0.2.0-alpha.6", true, false, a) + "]", ""},
		{"draft", "0.2.0", "[" + release("v0.2.1", false, true, a) + "]", ""},
		{"no assets", "0.2.0", "[" + release("v0.2.1", false, false, "[]") + "]", ""},
		{"wrong platform", "0.2.0", "[" + release("v0.2.1", false, false, asset("darwin", "arm64")) + "]", ""},
		{"bad version", "0.2.0", "[" + release("latest", false, false, a) + "]", ""},
		{"mismatched channel", "0.2.0", "[" + release("v0.2.1-alpha.1", false, false, a) + "]", ""},
		{"unsafe url", "0.2.0", "[" + release("v0.2.1", false, false, strings.ReplaceAll(a, "https://github.com/", "https://evil.example/")) + "]", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectRelease([]byte(tc.body), tc.current, "windows", "amd64")
			if err != nil || got.Published != tc.want || got.AutoInstall {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
	for _, body := range []string{`[] garbage`, `{}`, strings.Repeat(" ", (1<<20)+1), `[{`} {
		if _, err := selectRelease([]byte(body), "0.2.0", "windows", "amd64"); err == nil {
			t.Fatal("bad JSON/size accepted")
		}
	}
	if _, err := selectRelease([]byte(`[]`), "development", "windows", "amd64"); err == nil {
		t.Fatal("unknown current channel accepted")
	}
}
