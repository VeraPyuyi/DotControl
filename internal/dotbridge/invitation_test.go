package dotbridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func invitationFixture(t *testing.T) *Service {
	t.Helper()
	return &Service{root: physicalTemp(t), listen: "127.0.0.1:9317", state: PersistedState{Version: APIVersion, Clients: map[string]ClientPolicy{}}}
}

func TestDeviceInvitationLifecycle(t *testing.T) {
	for _, kind := range []string{"consume-restart", "wrong-device", "expired", "revoked", "reused", "concurrent", "invalid"} {
		t.Run(kind, func(t *testing.T) {
			s := invitationFixture(t)
			if kind == "invalid" {
				for _, device := range []string{"", "../escape"} {
					if _, e := s.CreateInvitation(device, time.Minute); e == nil {
						t.Fatal("invalid device accepted")
					}
				}
				if _, e := s.CreateInvitation("device.a", 0); e == nil {
					t.Fatal("zero expiry accepted")
				}
				return
			}
			token, e := s.CreateInvitation("device.a", time.Minute)
			if e != nil {
				t.Fatal(e)
			}
			if len(token) != 64 {
				t.Fatal("credential lacks 256-bit random encoding")
			}
			bytes, e := os.ReadFile(filepath.Join(s.root, "bridge.state.json"))
			if e != nil || strings.Contains(string(bytes), token) {
				t.Fatal("raw invitation persisted", e)
			}
			switch kind {
			case "wrong-device":
				if _, e = s.ConsumeInvitation(token, "device.b"); e == nil {
					t.Fatal("wrong device accepted")
				}
			case "expired":
				s.state.Invitations[hashToken(token)] = deviceInvitation{DeviceID: "device.a", Expires: time.Now().Add(-time.Second)}
				if _, e = s.ConsumeInvitation(token, "device.a"); e == nil {
					t.Fatal("expired invitation accepted")
				}
				return
			case "revoked":
				if e = s.RevokeInvitation(token); e != nil {
					t.Fatal(e)
				}
				if _, e = s.ConsumeInvitation(token, "device.a"); e == nil {
					t.Fatal("revoked invitation accepted")
				}
				return
			case "concurrent":
				var wg sync.WaitGroup
				successes := make(chan bool, 8)
				for i := 0; i < 8; i++ {
					wg.Add(1)
					go func() { defer wg.Done(); _, e := s.ConsumeInvitation(token, "device.a"); successes <- e == nil }()
				}
				wg.Wait()
				close(successes)
				n := 0
				for ok := range successes {
					if ok {
						n++
					}
				}
				if n != 1 {
					t.Fatalf("consumed %d times", n)
				}
				return
			}
			paired, e := s.ConsumeInvitation(token, "device.a")
			if e != nil || paired.ClientID != "device.a" || paired.Token == "" {
				t.Fatalf("%+v %v", paired, e)
			}
			if _, e = s.ConsumeInvitation(token, "device.a"); e == nil {
				t.Fatal("credential reused")
			}
			reopened := invitationFixture(t)
			reopened.root = s.root
			if e = reopened.loadState(); e != nil {
				t.Fatal(e)
			}
			if _, e = reopened.clientFromToken(paired.Token); e != nil {
				t.Fatal("identity lost on restart", e)
			}
			if _, e = reopened.ConsumeInvitation(token, "device.a"); e == nil {
				t.Fatal("reuse after restart")
			}
			if e = reopened.RevokeClient("device.a"); e != nil {
				t.Fatal(e)
			}
			if _, e = reopened.clientFromToken(paired.Token); e == nil {
				t.Fatal("revoked device accepted")
			}
		})
	}
}
func TestInvitationPersistenceFailureDoesNotConsume(t *testing.T) {
	s := invitationFixture(t)
	token, e := s.CreateInvitation("device.a", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(s.root, "bridge.state.json")
	before, _ := os.ReadFile(path)
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ConsumeInvitation(token, "device.a"); e == nil {
		t.Fatal("persistence failure ignored")
	}
	if len(s.state.Clients) != 0 {
		t.Fatal("uncommitted client remains authorized")
	}
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, before, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ConsumeInvitation(token, "device.a"); e != nil {
		t.Fatal("retry blocked", e)
	}
}
func TestPendingInvitationSurvivesRestart(t *testing.T) {
	s := invitationFixture(t)
	token, e := s.CreateInvitation("device.a", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	data, _ := json.Marshal(s.state)
	if strings.Contains(string(data), token) {
		t.Fatal("raw token leaked")
	}
	other := invitationFixture(t)
	other.root = s.root
	if e = other.loadState(); e != nil {
		t.Fatal(e)
	}
	if _, e = other.ConsumeInvitation(token, "device.a"); e != nil {
		t.Fatal(e)
	}
}
