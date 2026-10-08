// Package authproxy implements rclone's --auth-proxy protocol.
//
// rclone runs `rclone-proxy-tui auth-proxy <protocol>` for each new login,
// writes {"user": ..., "pass": ...} on stdin and expects a backend config on
// stdout. We look the user up in the state file, check the password and
// answer with a "combine" backend whose top level folders are the remotes
// shared with that client. Because the upstreams are named remotes from
// rclone.conf, crypt, alias, union etc. all keep working exactly as they do
// on the command line.
package authproxy

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/nosini/rclone-proxy-tui/internal/state"
)

// Request is what rclone sends on stdin.
type Request struct {
	User      string `json:"user"`
	Pass      string `json:"pass"`
	PublicKey string `json:"public_key"`
	ClientIP  string `json:"client_ip"`
}

// Resolve checks the credentials and returns the backend config to give
// rclone.
func Resolve(st *state.State, proto state.Protocol, req Request) (map[string]string, error) {
	c := st.ClientByUsername(req.User)
	if c == nil || c.Disabled {
		return nil, fmt.Errorf("login refused for %q from %s: unknown or disabled client", req.User, req.ClientIP)
	}
	cfg := map[string]string{}
	if proto == state.S3 {
		// S3 clients never send the secret, only a signature. rclone checks
		// the signature against the secret we hand back.
		cfg["_secret_access_key"] = c.Password
	} else {
		if req.PublicKey != "" {
			return nil, fmt.Errorf("login refused for %q from %s: public key login is not supported, use the password", req.User, req.ClientIP)
		}
		if req.Pass == "" || subtle.ConstantTimeCompare([]byte(req.Pass), []byte(c.Password)) != 1 {
			return nil, fmt.Errorf("login refused for %q from %s: wrong password", req.User, req.ClientIP)
		}
	}
	if len(c.Shares) == 0 {
		return nil, fmt.Errorf("login refused for %q: no remotes are shared with this client", req.User)
	}
	upstreams, err := Upstreams(c.Shares)
	if err != nil {
		return nil, err
	}
	cfg["type"] = "combine"
	cfg["upstreams"] = upstreams
	cfg["_root"] = ""
	return cfg, nil
}

// Upstreams builds the combine backend's upstreams option. Entries are
// space separated "folder=remote:path" and quoted CSV style when they
// contain spaces or quotes.
func Upstreams(shares []state.Share) (string, error) {
	seen := map[string]bool{}
	var parts []string
	for _, s := range shares {
		folder := s.FolderName()
		if folder == "" || s.Remote == "" {
			continue
		}
		if seen[folder] {
			return "", fmt.Errorf("two shares use the folder name %q", folder)
		}
		seen[folder] = true
		entry := folder + "=" + s.Target()
		if strings.ContainsAny(entry, " \"\t") {
			entry = `"` + strings.ReplaceAll(entry, `"`, `""`) + `"`
		}
		parts = append(parts, entry)
	}
	if len(parts) == 0 {
		return "", errors.New("no remotes to share")
	}
	return strings.Join(parts, " "), nil
}

// Run reads a request from in, resolves it and writes the answer to out.
func Run(paths state.Paths, proto state.Protocol, in io.Reader, out io.Writer) error {
	var req Request
	if err := json.NewDecoder(in).Decode(&req); err != nil {
		return fmt.Errorf("auth proxy: bad request: %w", err)
	}
	st, err := paths.Load()
	if err != nil {
		return fmt.Errorf("auth proxy: %w", err)
	}
	cfg, err := Resolve(st, proto, req)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(cfg)
}
