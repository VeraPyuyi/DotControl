package connections

import (
	"encoding/json"
	"testing"
)

func TestExecutionProfileApprovalPolicyIsExplicitAndTerminalOnly(t *testing.T) {
	for _, tc := range []struct {
		policy, execution string
		wantValid         bool
	}{
		{"", "terminal", true}, {"on-request", "terminal", true}, {"never", "terminal", true},
		{"", "computer_use", true}, {"on-request", "computer_use", true},
		{"never", "computer_use", false}, {"auto", "terminal", false},
	} {
		t.Run(tc.execution+"/"+tc.policy, func(t *testing.T) {
			p := execution(t)
			p.Execution = tc.execution
			p.AllowedApps = []string{"Calculator"}
			raw, _ := json.Marshal(map[string]string{"approval_policy": tc.policy})
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			if err := p.Validate(); (err == nil) != tc.wantValid {
				t.Fatalf("policy %q for %s validation = %v", tc.policy, tc.execution, err)
			}
			if !tc.wantValid {
				return
			}
			store := NewStore(t.TempDir())
			if _, err := store.UpsertExecution(p); err != nil {
				t.Fatal(err)
			}
			cfg, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(cfg.ExecutionProfiles[0])
			var fields map[string]any
			json.Unmarshal(data, &fields)
			if tc.policy == "" {
				if _, exists := fields["approval_policy"]; exists {
					t.Fatal("legacy policy was rewritten")
				}
			} else if fields["approval_policy"] != tc.policy {
				t.Fatal("explicit policy lost in local storage")
			}
		})
	}
}
