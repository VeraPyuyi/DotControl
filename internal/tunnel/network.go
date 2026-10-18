package tunnel

import (
	"context"
	"errors"
	"github.com/dotcontrol/dotcontrol/internal/config"
	"net"
	"net/url"
	"os"
	"strings"
)

type NetworkStatus struct {
	Mode   string `json:"mode"`
	Source string `json:"source"`
}
type resolvedNetwork struct {
	NetworkStatus
	proxyURL string
}
type systemProxy struct {
	URL, Bypass      string
	PAC, SOCKS, Auth bool
}

// String deliberately excludes the process-only URL, including under %+v.
func (r resolvedNetwork) String() string   { return r.Mode + ":" + r.Source }
func (r resolvedNetwork) GoString() string { return r.String() }

// ResolveNetwork inspects only proxy settings and returns safe diagnostic metadata.
// It never changes OS settings or opens a network connection.
func ResolveNetwork(ctx context.Context, n config.Network) (NetworkStatus, error) {
	r, err := resolveNetwork(ctx, n, os.LookupEnv, readSystemProxy)
	return r.NetworkStatus, err
}

func resolveNetwork(ctx context.Context, n config.Network, env func(string) (string, bool), system func(context.Context) (systemProxy, error)) (resolvedNetwork, error) {
	r := resolvedNetwork{NetworkStatus: NetworkStatus{Mode: n.Mode, Source: "direct"}}
	if r.Mode == "" {
		r.Mode = "auto"
	}
	if err := config.ValidateNetwork(n); err != nil {
		return r, err
	}
	if r.Mode == "direct" {
		return r, nil
	}
	noProxy := firstEnv(env, "NO_PROXY", "no_proxy")
	if bypassesControlPlane(noProxy) {
		r.Source = "bypass"
		return r, nil
	}
	if r.Mode == "manual" {
		r.Source = "manual"
		r.proxyURL = n.ProxyURL
		return r, nil
	}
	// The production control plane uses HTTPS. HTTP_PROXY and protocol-scoped
	// HTTP-only OS settings do not select a route for that destination.
	raw := firstEnv(env, "DOTCONTROL_CONTROL_PROXY", "CONTROL_PLANE_HTTP_PROXY", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy")
	if raw != "" {
		r.Source = "environment"
		if name, ok := strings.CutPrefix(raw, "env:"); ok {
			raw = firstEnv(env, name)
			if raw == "" {
				return r, errors.New("proxy_environment_missing")
			}
		}
		proxy, err := validatedProxy(raw, true)
		r.proxyURL = proxy
		return r, err
	}
	p, err := system(ctx)
	if err != nil {
		r.Source = "system"
		return r, errors.New("system_proxy_unavailable")
	}
	if bypassesControlPlane(p.Bypass) {
		r.Source = "bypass"
		return r, nil
	}
	if p.URL != "" {
		r.Source = "system"
		if p.Auth {
			return r, errors.New("requires_local_proxy_auth")
		}
		proxy, err := validatedProxy(p.URL, false)
		r.proxyURL = proxy
		return r, err
	}
	if p.PAC {
		r.Source = "system"
		return r, errors.New("proxy_pac_unsupported")
	}
	if p.SOCKS {
		r.Source = "system"
		return r, errors.New("proxy_socks_unsupported")
	}
	return r, nil
}

func firstEnv(env func(string) (string, bool), keys ...string) string {
	for _, k := range keys {
		if v, ok := env(k); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
func validatedProxy(raw string, allowCredentials bool) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("invalid_proxy_url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("proxy_scheme_unsupported")
	}
	if u.User != nil && !allowCredentials {
		return "", errors.New("requires_local_proxy_auth")
	}
	if u.Hostname() == "" || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || strings.ContainsAny(raw, "\r\n\t ") {
		return "", errors.New("invalid_proxy_url")
	}
	return u.String(), nil
}
func bypassesControlPlane(raw string) bool {
	const host = "api.openai.com"
	for _, entry := range strings.FieldsFunc(strings.ToLower(raw), func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		entry = strings.TrimSpace(entry)
		if entry == "*" {
			return true
		}
		if h, p, e := net.SplitHostPort(entry); e == nil {
			if p != "443" {
				continue
			}
			entry = h
		}
		entry = strings.TrimPrefix(entry, "*.")
		entry = strings.TrimPrefix(entry, ".")
		if entry != "" && (host == entry || strings.HasSuffix(host, "."+entry)) {
			return true
		}
	}
	return false
}
func (r resolvedNetwork) arguments() []string {
	if r.proxyURL == "" {
		return nil
	}
	return []string{"--control-plane.http-proxy", "env:DOTCONTROL_CONTROL_PROXY"}
}
func (r resolvedNetwork) environment(env []string) []string {
	result := make([]string, 0, len(env)+1)
	for _, s := range env {
		key, _, _ := strings.Cut(s, "=")
		key = strings.ToUpper(key)
		if strings.HasSuffix(key, "_PROXY") || key == "TUNNEL_CLIENT_HTTP_PROXY" {
			continue
		}
		result = append(result, s)
	}
	if r.proxyURL != "" {
		result = append(result, "DOTCONTROL_CONTROL_PROXY="+r.proxyURL)
	}
	return result
}
func parseWindowsProxy(server, bypass string, autodetect bool, pac string) systemProxy {
	p := systemProxy{Bypass: bypass, PAC: autodetect || pac != ""}
	values := map[string]string{}
	for _, part := range strings.Split(server, ";") {
		part = strings.TrimSpace(part)
		if k, v, ok := strings.Cut(part, "="); ok {
			values[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		} else if part != "" {
			values["all"] = part
		}
	}
	p.URL = values["https"]
	if p.URL == "" {
		p.URL = values["all"]
	}
	p.SOCKS = values["socks"] != "" || values["socks5"] != ""
	if p.URL != "" && !strings.Contains(p.URL, "://") {
		p.URL = "http://" + p.URL
	}
	return p
}
func parseSCUtilProxy(text string) systemProxy {
	values := map[string]string{}
	var bypass []string
	depth, rootDepth, exceptionDepth := 0, 0, -1
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "<dictionary> {" && depth == 0 {
			depth = 1
			rootDepth = 1
			continue
		}
		if line == "}" {
			if depth == exceptionDepth {
				exceptionDepth = -1
			}
			depth--
			continue
		}
		if strings.HasSuffix(line, "{") {
			if depth == rootDepth && strings.HasPrefix(line, "ExceptionsList") {
				exceptionDepth = depth + 1
			}
			depth++
			continue
		}
		k, v, ok := strings.Cut(line, " : ")
		if !ok {
			continue
		}
		if depth == exceptionDepth {
			bypass = append(bypass, strings.TrimSpace(v))
		} else if depth == rootDepth {
			values[k] = strings.TrimSpace(v)
		}
	}
	p := systemProxy{Bypass: strings.Join(bypass, ";"), PAC: values["ProxyAutoConfigEnable"] == "1" || values["ProxyAutoDiscoveryEnable"] == "1", SOCKS: values["SOCKSEnable"] == "1"}
	for _, prefix := range []string{"HTTPS"} {
		if values[prefix+"Enable"] != "1" {
			continue
		}
		host, port := values[prefix+"Proxy"], values[prefix+"Port"]
		if host == "" || port == "" {
			p.URL = "invalid:"
			return p
		}
		p.URL = "http://" + net.JoinHostPort(host, port)
		p.Auth = values[prefix+"User"] != "" || values[prefix+"Password"] != "" || values[prefix+"AuthEnable"] == "1" || values[prefix+"ProxyAuthenticationRequired"] == "1" || values[prefix+"ProxyAuthenticated"] == "1"
		return p
	}
	return p
}
