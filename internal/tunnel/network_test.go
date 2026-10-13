package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/config"
)

func TestNetworkResolutionPrecedenceAndBypass(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		mode                           config.Network
		env                            map[string]string
		system                         systemProxy
		wantSource, wantProxy, wantErr string
	}{
		{name: "auto default direct", wantSource: "direct"},
		{name: "explicit direct ignores invalid env", mode: config.Network{Mode: "direct"}, env: map[string]string{"HTTPS_PROXY": "socks5://secret"}, wantSource: "direct"},
		{name: "control env priority", env: map[string]string{"CONTROL_PLANE_HTTP_PROXY": "http://one:80", "HTTPS_PROXY": "http://two:80"}, system: systemProxy{URL: "http://three:80"}, wantSource: "environment", wantProxy: "http://one:80"},
		{name: "https environment", env: map[string]string{"https_proxy": "http://one:80"}, wantSource: "environment", wantProxy: "http://one:80"},
		{name: "static user proxy", system: systemProxy{URL: "http://three:80"}, wantSource: "system", wantProxy: "http://three:80"},
		{name: "env bypass", env: map[string]string{"HTTPS_PROXY": "http://one:80", "NO_PROXY": ".openai.com"}, system: systemProxy{URL: "http://three:80"}, wantSource: "bypass"},
		{name: "system bypass", system: systemProxy{URL: "http://three:80", Bypass: "*.openai.com;<local>"}, wantSource: "bypass"},
		{name: "manual", mode: config.Network{Mode: "manual", ProxyURL: "https://local.test:8443"}, wantSource: "manual", wantProxy: "https://local.test:8443"},
		{name: "manual respects bypass", mode: config.Network{Mode: "manual", ProxyURL: "https://local.test:8443"}, env: map[string]string{"NO_PROXY": "api.openai.com:443"}, wantSource: "bypass"},
		{name: "PAC blocked", system: systemProxy{PAC: true}, wantErr: "proxy_pac_unsupported"},
		{name: "SOCKS blocked", system: systemProxy{SOCKS: true}, wantErr: "proxy_socks_unsupported"},
		{name: "auth blocked", system: systemProxy{URL: "http://user:pass@proxy:80"}, wantErr: "requires_local_proxy_auth"},
		{name: "no invalid env fallback", env: map[string]string{"HTTPS_PROXY": "socks5://one:80"}, system: systemProxy{URL: "http://three:80"}, wantErr: "proxy_scheme_unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := resolveNetwork(context.Background(), tc.mode, func(k string) (string, bool) { v, ok := tc.env[k]; return v, ok }, func(context.Context) (systemProxy, error) { return tc.system, nil })
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("error %v want %s", err, tc.wantErr)
				}
				return
			}
			if err != nil || r.Source != tc.wantSource || r.proxyURL != tc.wantProxy {
				t.Fatalf("resolution %+v error %v", r, err)
			}
		})
	}
}

func TestMacInteractiveAuthIsBlocked(t *testing.T) {
	p := parseSCUtilProxy("HTTPSEnable : 1\nHTTPSProxy : secure\nHTTPSPort : 8443\nHTTPSProxyAuthenticationRequired : 1")
	_, err := resolveNetwork(context.Background(), config.Network{}, func(string) (string, bool) { return "", false }, func(context.Context) (systemProxy, error) { return p, nil })
	if err == nil || err.Error() != "requires_local_proxy_auth" {
		t.Fatalf("authentication must be explicit: %v", err)
	}
}

func TestProxySecretsNeverEnterDiagnosticsOrArguments(t *testing.T) {
	r, err := resolveNetwork(context.Background(), config.Network{}, func(k string) (string, bool) { return "http://user:VERY_PRIVATE@proxy:8080", k == "HTTPS_PROXY" }, func(context.Context) (systemProxy, error) { return systemProxy{}, errors.New("must not read system") })
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r)
	for _, s := range []string{string(b), fmt.Sprintf("%+v", r), fmt.Sprintf("%#v", r), strings.Join(r.arguments(), " ")} {
		if strings.Contains(s, "VERY_PRIVATE") || strings.Contains(s, "user:") {
			t.Fatal("proxy credentials leaked")
		}
	}
	env := r.environment([]string{"HTTPS_PROXY=http://other:80", "http_proxy=http://other:80", "NO_PROXY=*", "MCP_HTTP_PROXY=http://other:80", "DOTCONTROL_CONTROL_PROXY=old", "PATH=ok"})
	if len(env) != 2 || env[0] != "PATH=ok" || env[1] != "DOTCONTROL_CONTROL_PROXY=http://user:VERY_PRIVATE@proxy:8080" {
		t.Fatal("proxy environment was not confined")
	}
	if strings.Join(r.arguments(), " ") != "--control-plane.http-proxy env:DOTCONTROL_CONTROL_PROXY" {
		t.Fatal("expected scoped env reference")
	}
}

func TestSystemProxyParsers(t *testing.T) {
	w := parseWindowsProxy("http=web:8080;https=secure:8443;socks=socks:1080", "*.example.test", false, "")
	if w.URL != "http://secure:8443" || w.Bypass != "*.example.test" {
		t.Fatal(w)
	}
	mac := parseSCUtilProxy(`<dictionary> {
 HTTPEnable : 1
 HTTPProxy : web
 HTTPPort : 8080
 HTTPSEnable : 1
 HTTPSProxy : secure
 HTTPSPort : 8443
 ExceptionsList : <array> {
   0 : *.example.test
   1 : localhost
 }
}`)
	if mac.URL != "http://secure:8443" || !strings.Contains(mac.Bypass, "*.example.test") {
		t.Fatal(mac)
	}
	if !parseSCUtilProxy("ProxyAutoConfigEnable : 1\nProxyAutoConfigURLString : http://secret/pac").PAC {
		t.Fatal("PAC lost")
	}
}

func TestMacScopedProxyDoesNotOverrideDefaultRoute(t *testing.T) {
	p := parseSCUtilProxy(`<dictionary> {
 HTTPSEnable : 1
 HTTPSProxy : global
 HTTPSPort : 8443
 __SCOPED__ : <dictionary> {
  en9 : <dictionary> {
   HTTPSEnable : 1
   HTTPSProxy : different
   HTTPSPort : 9999
  }
 }
}`)
	if p.URL != "http://global:8443" {
		t.Fatalf("scoped route overrode default: %s", p.URL)
	}
}
