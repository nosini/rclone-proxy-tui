// Package rclone drives the rclone binary: listing and editing remotes in
// the normal rclone.conf and building `rclone serve` command lines.
package rclone

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Rclone is a handle on an rclone binary and config file.
type Rclone struct {
	Bin    string
	Config string
}

// ErrNotFound means no rclone binary could be found.
var ErrNotFound = errors.New("rclone binary not found - install it from https://rclone.org/install/ or set its path in the Server tab")

// DetectBinary finds rclone on PATH or in common locations.
func DetectBinary() (string, error) {
	if p, err := exec.LookPath("rclone"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs, nil
		}
		return p, nil
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{"/usr/bin/rclone", "/usr/local/bin/rclone", filepath.Join(home, "bin", "rclone"), filepath.Join(home, ".local", "bin", "rclone"), filepath.Join(home, "go", "bin", "rclone")} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", ErrNotFound
}

// Command builds an exec.Cmd for rclone with the config file set.
func (r *Rclone) Command(ctx context.Context, args ...string) *exec.Cmd {
	full := args
	if r.Config != "" {
		full = append([]string{"--config", r.Config}, args...)
	}
	if ctx == nil {
		return exec.Command(r.Bin, full...)
	}
	return exec.CommandContext(ctx, r.Bin, full...)
}

func (r *Rclone) run(ctx context.Context, args ...string) ([]byte, error) {
	if r.Bin == "" {
		return nil, ErrNotFound
	}
	var stdout, stderr bytes.Buffer
	cmd := r.Command(ctx, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg != "" {
			return stdout.Bytes(), fmt.Errorf("%s", lastLines(msg, 4))
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func timeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// Version returns the first line of `rclone version`.
func (r *Rclone) Version() (string, error) {
	ctx, cancel := timeout(15 * time.Second)
	defer cancel()
	out, err := r.run(ctx, "version")
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(line), nil
}

// DefaultConfigFile returns the config file rclone uses by default.
func DefaultConfigFile(bin string) (string, error) {
	r := &Rclone{Bin: bin}
	ctx, cancel := timeout(15 * time.Second)
	defer cancel()
	out, err := r.run(ctx, "config", "file")
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(lines[len(lines)-1]), nil
}

// Remote is a configured remote from rclone.conf.
type Remote struct {
	Name   string
	Type   string
	Config map[string]string
}

// Detail is a short description of where the remote points.
func (r Remote) Detail() string {
	c := r.Config
	switch r.Type {
	case "crypt", "alias", "chunker", "compress", "hasher", "cache":
		return "→ " + c["remote"]
	case "union", "combine":
		return "→ " + c["upstreams"]
	case "sftp", "ftp", "smb":
		h := c["host"]
		if u := c["user"]; u != "" {
			h = u + "@" + h
		}
		return h
	case "webdav", "http":
		return c["url"]
	case "s3":
		s := c["provider"]
		if e := c["endpoint"]; e != "" {
			s += " " + e
		}
		return s
	case "local":
		return "local disk"
	}
	return ""
}

// Remotes lists the configured remotes sorted by name.
func (r *Rclone) Remotes() ([]Remote, error) {
	ctx, cancel := timeout(30 * time.Second)
	defer cancel()
	out, err := r.run(ctx, "config", "dump")
	if err != nil {
		return nil, err
	}
	var dump map[string]map[string]string
	if err := json.Unmarshal(out, &dump); err != nil {
		return nil, fmt.Errorf("parse rclone config dump: %w", err)
	}
	var remotes []Remote
	for name, cfg := range dump {
		remotes = append(remotes, Remote{Name: name, Type: cfg["type"], Config: cfg})
	}
	sort.Slice(remotes, func(i, j int) bool {
		return strings.ToLower(remotes[i].Name) < strings.ToLower(remotes[j].Name)
	})
	return remotes, nil
}

// Example is one suggested value for an option.
type Example struct {
	Value    string
	Help     string
	Provider string
}

// Option is one config option of a backend as reported by
// `rclone config providers`.
type Option struct {
	Name       string
	Help       string
	Provider   string
	DefaultStr string
	Examples   []Example
	Hide       int
	Required   bool
	IsPassword bool
	Advanced   bool
	Exclusive  bool
	Sensitive  bool
	Type       string
}

// Backend is a storage system type rclone supports.
type Backend struct {
	Name        string
	Description string
	Prefix      string
	Options     []Option
	Hide        bool
}

// NeedsOAuth reports whether setting up the backend usually involves a
// browser based login.
func (b Backend) NeedsOAuth() bool {
	for _, o := range b.Options {
		if o.Name == "token" {
			return true
		}
	}
	return false
}

var (
	providersMu    sync.Mutex
	providersCache = map[string][]Backend{}
)

// Backends lists the backend types rclone supports.
func (r *Rclone) Backends() ([]Backend, error) {
	providersMu.Lock()
	defer providersMu.Unlock()
	if b, ok := providersCache[r.Bin]; ok {
		return b, nil
	}
	ctx, cancel := timeout(30 * time.Second)
	defer cancel()
	out, err := r.run(ctx, "config", "providers")
	if err != nil {
		return nil, err
	}
	var all []Backend
	if err := json.Unmarshal(out, &all); err != nil {
		return nil, fmt.Errorf("parse rclone config providers: %w", err)
	}
	var backends []Backend
	for _, b := range all {
		if !b.Hide {
			backends = append(backends, b)
		}
	}
	sort.Slice(backends, func(i, j int) bool { return backends[i].Name < backends[j].Name })
	providersCache[r.Bin] = backends
	return backends, nil
}

// ConfigResult is what `rclone config create/update --non-interactive`
// returns. A non-empty State means more interactive questions remain
// (typically an OAuth login).
type ConfigResult struct {
	State  string
	Error  string
	Option *Option
}

// NeedsMore reports whether the remote needs interactive setup to finish.
func (c *ConfigResult) NeedsMore() bool {
	return c != nil && c.State != ""
}

func kvArgs(kv map[string]string) []string {
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var args []string
	for _, k := range keys {
		args = append(args, k+"="+kv[k])
	}
	return args
}

func (r *Rclone) configCmd(verb string, args []string, kv map[string]string) (*ConfigResult, error) {
	ctx, cancel := timeout(60 * time.Second)
	defer cancel()
	full := append([]string{"config", verb, "--non-interactive", "--obscure", "--"}, args...)
	full = append(full, kvArgs(kv)...)
	out, err := r.run(ctx, full...)
	if err != nil {
		return nil, err
	}
	res := &ConfigResult{}
	if len(bytes.TrimSpace(out)) > 0 {
		if err := json.Unmarshal(out, res); err != nil {
			return nil, fmt.Errorf("parse rclone output: %w", err)
		}
	}
	if res.Error != "" {
		return res, errors.New(res.Error)
	}
	return res, nil
}

// Create makes a new remote. Plain text passwords in kv are obscured.
func (r *Rclone) Create(name, typ string, kv map[string]string) (*ConfigResult, error) {
	return r.configCmd("create", []string{name, typ}, kv)
}

// Update changes options on a remote. Plain text passwords in kv are
// obscured.
func (r *Rclone) Update(name string, kv map[string]string) (*ConfigResult, error) {
	return r.configCmd("update", []string{name}, kv)
}

// Delete removes a remote from the config.
func (r *Rclone) Delete(name string) error {
	ctx, cancel := timeout(30 * time.Second)
	defer cancel()
	_, err := r.run(ctx, "config", "delete", "--", name)
	return err
}

// Test lists the top of a remote to check it works.
func (r *Rclone) Test(name string) (string, error) {
	ctx, cancel := timeout(45 * time.Second)
	defer cancel()
	start := time.Now()
	out, err := r.run(ctx, "lsf", "--max-depth", "1", "--", name+":")
	if ctx.Err() != nil {
		return "", fmt.Errorf("timed out after 45s")
	}
	if err != nil {
		return "", err
	}
	n := 0
	var sample []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l == "" {
			continue
		}
		n++
		if len(sample) < 8 {
			sample = append(sample, "  "+l)
		}
	}
	msg := fmt.Sprintf("%s: OK, %d entries at the top level (%.1fs)", name, n, time.Since(start).Seconds())
	if len(sample) > 0 {
		msg += "\n\n" + strings.Join(sample, "\n")
		if n > len(sample) {
			msg += "\n  …"
		}
	}
	return msg, nil
}

var (
	authProxyMu    sync.Mutex
	authProxyCache = map[string]bool{}
)

// SupportsAuthProxy reports whether `rclone serve <proto>` has
// --auth-proxy. S3 gained it later than the others.
func (r *Rclone) SupportsAuthProxy(proto string) bool {
	authProxyMu.Lock()
	defer authProxyMu.Unlock()
	key := r.Bin + "\x00" + proto
	if v, ok := authProxyCache[key]; ok {
		return v
	}
	ctx, cancel := timeout(15 * time.Second)
	defer cancel()
	out, err := r.run(ctx, "serve", proto, "--help")
	v := err == nil && strings.Contains(string(out), "--auth-proxy string")
	if err == nil {
		authProxyCache[key] = v
	}
	return v
}
