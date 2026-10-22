package dotqueue

import "testing"

func TestArtifactHostNumericAliases(t *testing.T) {
	cases := []struct {
		url     string
		allowed bool
	}{
		{"https://0x7f.0.0.1/a", false},
		{"https://0xa.0.0.1/a", false},
		{"https://0X7F.0X0.0.1/a", false},
		{"https://0x7f.1/a", false},
		{"https://0x7f000001/a", false},
		{"https://0x7f000001./a", false},
		{"https://0x7f.0.0.1./a", false},
		{"https://0177.0.0.1/a", false},
		{"https://0177.1./a", false},
		{"https://127.1/a", false},
		{"https://127.0.1./a", false},
		{"https://2130706433/a", false},
		{"https://2130706433./a", false},
		{"https://0x08080808/a", false},
		{"https://010.010.010.010/a", false},
		{"https://example.123/a", false},
		{"https://example.0x7f/a", false},
		{"https://example.0x/a", false},
		{"https://[::ffff:127.0.0.1]/a", false},
		{"https://[::ffff:7f00:1]/a", false},
		{"https://[::ffff:8.8.8.8]/a", false},
		{"https://[::ffff:808:808]/a", false},
		{"https://[fe80::1%25eth0]/a", false},
		{"https://[2606:4700:4700::1111%25eth0]/a", false},
		{"https://example.com/a", true},
		{"https://Example.COM./a", true},
		{"https://cdn123.example.com/a", true},
		{"https://0x7f.example.com/a", true},
		{"https://123.example.com/a", true},
		{"https://example.0xg/a", true},
		{"https://example.123abc/a", true},
		{"https://8.8.8.8/a", true},
		{"https://8.8.8.8./a", true},
		{"https://[2606:4700:4700::1111]/a", true},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			err := validateArtifact(Artifact{URL: tc.url, Label: "artifact"})
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v, got error %v", tc.allowed, err)
			}
		})
	}
}
