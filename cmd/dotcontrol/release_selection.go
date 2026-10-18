package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Deliberately supports only the project's stable and alpha channels. Unknown
// channels require an explicit policy rather than silently becoming stable.
type releaseVersion struct {
	major, minor, patch, alpha int
	channel                    string
}

var releasePattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-alpha\.(0|[1-9][0-9]*))?$`)

func parseReleaseVersion(s string) (releaseVersion, bool) {
	m := releasePattern.FindStringSubmatch(s)
	if m == nil {
		return releaseVersion{}, false
	}
	v := releaseVersion{channel: "stable"}
	fields := []*int{&v.major, &v.minor, &v.patch, &v.alpha}
	for i, p := range fields {
		if m[i+1] == "" {
			continue
		}
		n, e := strconv.Atoi(m[i+1])
		if e != nil {
			return releaseVersion{}, false
		}
		*p = n
	}
	if m[4] != "" {
		v.channel = "alpha"
	}
	return v, true
}
func (v releaseVersion) newer(w releaseVersion) bool {
	a, b := []int{v.major, v.minor, v.patch, v.alpha}, []int{w.major, w.minor, w.patch, w.alpha}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

type releaseRecommendation struct {
	Current      string `json:"current"`
	Published    string `json:"published"`
	Channel      string `json:"channel"`
	Verification string `json:"verification"`
	AutoInstall  bool   `json:"auto_install"`
}
type publicRelease struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func selectRelease(body []byte, current, goos, arch string) (releaseRecommendation, error) {
	result := releaseRecommendation{Current: current, Verification: "publisher_signature_pending"}
	base, ok := parseReleaseVersion(current)
	if !ok {
		return result, errors.New("update_channel_unknown")
	}
	result.Channel = base.channel
	if len(body) > 1<<20 {
		return result, errors.New("update_response_too_large")
	}
	var releases []publicRelease
	if json.Unmarshal(body, &releases) != nil {
		return result, errors.New("update_response_invalid")
	}
	best := base
	for _, r := range releases {
		v, ok := parseReleaseVersion(r.Tag)
		if !ok || r.Draft || v.channel != base.channel || r.Prerelease != (v.channel != "stable") || !v.newer(best) {
			continue
		}
		for _, a := range r.Assets {
			u, e := url.Parse(a.URL)
			if e != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/VeraPyuyi/DotControl/releases/download/"+r.Tag+"/") {
				continue
			}
			platform := goos
			if platform == "darwin" {
				platform = "macos"
			}
			if !strings.HasPrefix(a.Name, "DotControl-") || !strings.Contains(a.Name, "-"+platform+"-"+arch) {
				continue
			}
			if !strings.HasSuffix(a.Name, ".zip") && !strings.HasSuffix(a.Name, "-setup.exe") && !strings.HasSuffix(a.Name, ".dmg") {
				continue
			}
			best = v
			result.Published = r.Tag
			break
		}
	}
	return result, nil
}
