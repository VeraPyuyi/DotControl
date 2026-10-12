//go:build darwin

package desktop

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const agentLabel = "org.dotcontrol.agent"
const permissionHost = "org.dotcontrol.menu"

type agentJob struct {
	Label   string
	Args    []string
	Program string
}
type jobDictionary struct{ Values map[string]any }

func (d *jobDictionary) UnmarshalXML(dec *xml.Decoder, start xml.StartElement) error {
	d.Values = map[string]any{}
	for {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := t.(type) {
		case xml.EndElement:
			if t.Name == start.Name {
				return nil
			}
		case xml.StartElement:
			if t.Name.Local != "key" {
				return errors.New("invalid_plist")
			}
			var key string
			if dec.DecodeElement(&key, &t) != nil {
				return errors.New("invalid_plist")
			}
			if _, exists := d.Values[key]; exists {
				return errors.New("invalid_plist")
			}
			for {
				v, err := dec.Token()
				if err != nil {
					return err
				}
				if value, ok := v.(xml.StartElement); ok {
					switch value.Name.Local {
					case "string":
						var s string
						if err := dec.DecodeElement(&s, &value); err != nil {
							return err
						}
						d.Values[key] = s
					case "array":
						var a struct {
							Strings []string `xml:"string"`
						}
						if err := dec.DecodeElement(&a, &value); err != nil {
							return err
						}
						d.Values[key] = a.Strings
					default:
						d.Values[key] = nil
						if err := dec.Skip(); err != nil {
							return err
						}
					}
					break
				}
				if _, ok := v.(xml.EndElement); ok {
					return errors.New("invalid_plist")
				}
			}
		}
	}
}
func parseJob(data []byte) (agentJob, error) {
	var p struct {
		XMLName xml.Name      `xml:"plist"`
		Dict    jobDictionary `xml:"dict"`
	}
	if len(data) > 65536 || xml.Unmarshal(data, &p) != nil {
		return agentJob{}, errors.New("autostart_ownership_mismatch")
	}
	label, _ := p.Dict.Values["Label"].(string)
	args, _ := p.Dict.Values["ProgramArguments"].([]string)
	_, override := p.Dict.Values["Program"]
	if label != agentLabel || override || len(args) != 4 || args[1] != "--state" || args[3] != "serve" || !filepath.IsAbs(args[0]) || !filepath.IsAbs(args[2]) || filepath.Base(args[0]) != "dotcontrol" {
		return agentJob{}, errors.New("autostart_ownership_mismatch")
	}
	for _, s := range args {
		if strings.ContainsAny(s, "\r\n\x00") {
			return agentJob{}, errors.New("autostart_ownership_mismatch")
		}
	}
	return agentJob{label, args, args[0]}, nil
}
func jobData(executable, state string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Label</key><string>` + agentLabel + `</string><key>ProgramArguments</key><array><string>` + xmlText(executable) + `</string><string>--state</string><string>` + xmlText(state) + `</string><string>serve</string></array><key>AssociatedBundleIdentifiers</key><array><string>` + permissionHost + `</string></array><key>RunAtLoad</key><true/><key>ProcessType</key><string>Interactive</string><key>LimitLoadToSessionType</key><string>Aqua</string><key>ThrottleInterval</key><integer>15</integer></dict></plist>`)
}

// Tests inject a fake launchctl and a temporary definition. They never load or
// stop the signed-in user's service. No launchctl output escapes this package.
type launchAgent struct {
	path string
	uid  int
	run  func(...string) ([]byte, error)
}

func localAgent() launchAgent { return launchAgent{plistPath(), os.Getuid(), launchctl} }

type commandOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *commandOutput) Write(p []byte) (int, error) {
	n := len(p)
	left := 65536 - b.Len()
	if n > left {
		b.overflow = true
		p = p[:left]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}
func launchctl(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/launchctl", args...)
	var out commandOutput
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if out.overflow || ctx.Err() != nil {
		return nil, errors.New("autostart_query_failed")
	}
	return out.Bytes(), err
}
func (m launchAgent) domain() string { return fmt.Sprintf("gui/%d", m.uid) }
func (m launchAgent) target() string { return m.domain() + "/" + agentLabel }
func (m launchAgent) read() (agentJob, []byte, error) {
	info, err := os.Lstat(m.path)
	if err != nil {
		return agentJob{}, nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || st.Uid != uint32(m.uid) || st.Nlink != 1 || info.Mode().Perm()&0o022 != 0 {
		return agentJob{}, nil, errors.New("autostart_ownership_mismatch")
	}
	fd, err := unix.Open(m.path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return agentJob{}, nil, err
	}
	f := os.NewFile(uintptr(fd), "launch_agent_definition")
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return agentJob{}, nil, errors.New("autostart_ownership_mismatch")
	}
	data, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil {
		return agentJob{}, nil, err
	}
	j, err := parseJob(data)
	return j, data, err
}
func (m launchAgent) disabled() (bool, error) {
	data, err := m.run("print-disabled", m.domain())
	if err != nil {
		return false, errors.New("autostart_query_failed")
	}
	pattern := regexp.MustCompile(`(?m)^\s*"` + regexp.QuoteMeta(agentLabel) + `"\s*=>\s*(true|false)\s*$`)
	matches := pattern.FindAllSubmatch(data, -1)
	if len(matches) > 1 {
		return false, errors.New("autostart_query_failed")
	}
	return len(matches) == 1 && string(matches[0][1]) == "true", nil
}
func launchField(data, key string) string {
	for _, line := range strings.Split(data, "\n") {
		if value, found := strings.CutPrefix(strings.TrimSpace(line), key+" = "); found {
			return strings.Trim(value, `"`)
		}
	}
	return ""
}
func (m launchAgent) loaded(j agentJob) (bool, bool, error) {
	data, err := m.run("print", m.target())
	if err != nil {
		if strings.Contains(string(data), "Could not find service") {
			return false, false, nil
		}
		return false, false, errors.New("autostart_query_failed")
	}
	text := string(data)
	if j.Label == "" || !strings.HasPrefix(strings.TrimSpace(text), m.target()+" = {") || launchField(text, "path") != m.path || launchField(text, "program") != j.Program {
		return false, false, errors.New("autostart_ownership_mismatch")
	}
	var args []string
	reading := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "arguments = {" {
			if reading || args != nil {
				return false, false, errors.New("autostart_ownership_mismatch")
			}
			reading = true
			continue
		}
		if reading {
			if line == "}" {
				reading = false
			} else {
				args = append(args, strings.Trim(line, `"`))
			}
		}
	}
	if reading || len(args) != len(j.Args) {
		return false, false, errors.New("autostart_ownership_mismatch")
	}
	for i := range args {
		if args[i] != j.Args[i] {
			return false, false, errors.New("autostart_ownership_mismatch")
		}
	}
	return true, launchField(text, "state") == "running", nil
}
func (m launchAgent) write(data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(m.path), ".dotcontrol-agent-*")
	if err != nil {
		return errors.New("autostart_unavailable")
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errors.New("autostart_unavailable")
	}
	return os.Rename(f.Name(), m.path)
}
func (m launchAgent) lock() (func(), error) {
	if os.MkdirAll(filepath.Dir(m.path), 0700) != nil {
		return nil, errors.New("autostart_unavailable")
	}
	fd, err := unix.Open(m.path+".lock", unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, errors.New("autostart_unavailable")
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uint32(m.uid) || st.Nlink != 1 || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0077 != 0 || unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		unix.Close(fd)
		return nil, errors.New("autostart_busy")
	}
	return func() { _ = unix.Close(fd) }, nil
}
func (m launchAgent) start(executable, state string, replace bool) error {
	if _, err := parseJob(jobData(executable, state)); err != nil {
		return err
	}
	unlock, err := m.lock()
	if err != nil {
		return err
	}
	defer unlock()
	disabled, err := m.disabled()
	if err != nil {
		return err
	}
	if disabled {
		return errors.New("autostart_disabled")
	}
	old, backup, err := m.read()
	if err != nil && !os.IsNotExist(err) {
		return errors.New("autostart_ownership_mismatch")
	}
	if os.IsNotExist(err) && !replace {
		return errors.New("autostart_not_configured")
	}
	if old.Label != "" && (old.Args[2] != state || (old.Program != executable && (!replace || filepath.Dir(filepath.Dir(old.Program)) != filepath.Dir(filepath.Dir(executable))))) {
		return errors.New("autostart_ownership_mismatch")
	}
	loaded, running, err := m.loaded(old)
	if err != nil {
		return err
	}
	if loaded && !replace && old.Program == executable {
		if running {
			return nil
		}
		if _, err = m.run("kickstart", m.target()); err != nil {
			return errors.New("autostart_start_failed")
		}
		return nil
	}
	if loaded {
		if _, err = m.run("bootout", m.target()); err != nil {
			return errors.New("autostart_unload_failed")
		}
	}
	if !bytes.Equal(backup, jobData(executable, state)) {
		if err = m.write(jobData(executable, state)); err != nil {
			return err
		}
	}
	if _, err = m.run("bootstrap", m.domain(), m.path); err == nil {
		job, _, readErr := m.read()
		registered, _, checkErr := m.loaded(job)
		if readErr == nil && checkErr == nil && registered {
			return nil
		}
	}
	// Keep the target definition if registration is uncertain. The install
	// transaction rolls back via this same path, checking loaded ownership
	// before unloading anything. Restoring the old plist here could disguise
	// a partially loaded new job and make safe rollback impossible.
	return errors.New("autostart_load_failed")
}
func (m launchAgent) remove(state string) error {
	unlock, err := m.lock()
	if err != nil {
		return err
	}
	defer unlock()
	j, _, err := m.read()
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || j.Args[2] != state {
		return errors.New("autostart_ownership_mismatch")
	}
	loaded, _, err := m.loaded(j)
	if err != nil {
		return err
	}
	if loaded {
		if _, err = m.run("bootout", m.target()); err != nil {
			return errors.New("autostart_unload_failed")
		}
	}
	return os.Remove(m.path)
}
func (m launchAgent) status(executable, state string) StartupStatus {
	s := StartupStatus{Reason: "not_configured"}
	j, _, err := m.read()
	if os.IsNotExist(err) {
		return s
	}
	s.Configured = true
	if err != nil || (executable != "" && j.Program != executable) || (state != "" && j.Args[2] != state) {
		s.Reason = "ownership_mismatch"
		return s
	}
	s.ProgramMatches = true
	s.Disabled, err = m.disabled()
	if err != nil {
		s.Reason = "query_failed"
		return s
	}
	if s.Disabled {
		s.Reason = "disabled_by_user"
		return s
	}
	s.Registered, s.Running, err = m.loaded(j)
	if err != nil {
		s.ProgramMatches = false
		s.Reason = "query_failed"
		return s
	}
	if s.Registered {
		s.Reason = "registered"
	} else {
		s.Reason = "not_registered"
	}
	return s
}
func StartAgent(executable, state string) error { return localAgent().start(executable, state, true) }

// Refuse disabled or unowned jobs before installation stops any old agent.
func (m launchAgent) validate(state string) error {
	disabled, err := m.disabled()
	if err != nil {
		return err
	}
	if disabled {
		return errors.New("autostart_disabled")
	}
	j, _, err := m.read()
	if err != nil && !os.IsNotExist(err) {
		return errors.New("autostart_ownership_mismatch")
	}
	if err == nil && j.Args[2] != state {
		return errors.New("autostart_ownership_mismatch")
	}
	_, _, err = m.loaded(j)
	return err
}
func ValidateAutostart(state string) error       { return localAgent().validate(state) }
func EnsureAgent(executable, state string) error { return localAgent().start(executable, state, false) }
func AutostartStatus(executable, state string) StartupStatus {
	return localAgent().status(executable, state)
}
