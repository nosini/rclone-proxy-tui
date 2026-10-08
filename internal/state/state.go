// Package state holds the persistent configuration shared by the TUI, the
// background daemon and the auth proxy.
//
// The TUI is the only writer. The daemon and the auth proxy only read, and
// every write is an atomic rename so readers never see a partial file.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Protocol is a protocol rclone can serve remotes over.
type Protocol string

// Supported protocols. All of them support rclone's --auth-proxy, which is
// what lets many clients share one port.
const (
	WebDAV Protocol = "webdav"
	SFTP   Protocol = "sftp"
	S3     Protocol = "s3"
	FTP    Protocol = "ftp"
	HTTP   Protocol = "http"
)

// AllProtocols in display order.
var AllProtocols = []Protocol{WebDAV, SFTP, S3, FTP, HTTP}

// Title is the human readable protocol name.
func (p Protocol) Title() string {
	switch p {
	case WebDAV:
		return "WebDAV"
	case SFTP:
		return "SFTP"
	case S3:
		return "S3"
	case FTP:
		return "FTP"
	case HTTP:
		return "HTTP"
	}
	return string(p)
}

// Description is a one line explanation shown in the Server tab.
func (p Protocol) Description() string {
	switch p {
	case WebDAV:
		return "file managers, phones, Windows drive mapping, rclone"
	case SFTP:
		return "WinSCP, FileZilla, sshfs, rclone"
	case S3:
		return "S3 API: access key = username, secret = password"
	case FTP:
		return "legacy clients (unencrypted unless TLS is set)"
	case HTTP:
		return "read-only browsing in a web browser"
	}
	return ""
}

// SupportsTLS reports whether --cert/--key apply to this protocol.
func (p Protocol) SupportsTLS() bool {
	return p != SFTP
}

// DefaultPort for each protocol.
func (p Protocol) DefaultPort() int {
	switch p {
	case WebDAV:
		return 8080
	case SFTP:
		return 2022
	case S3:
		return 8333
	case FTP:
		return 2121
	case HTTP:
		return 8081
	}
	return 0
}

// ProtocolConfig is the server side configuration of one protocol.
type ProtocolConfig struct {
	Enabled    bool   `json:"enabled"`
	Port       int    `json:"port"`
	ExtraFlags string `json:"extra_flags,omitempty"`
}

// Share is one remote exposed to a client. It appears to the client as a
// top level folder called Folder.
type Share struct {
	Remote string `json:"remote"`
	Path   string `json:"path,omitempty"`
	Folder string `json:"folder,omitempty"`
}

// FolderName is the folder the share appears as.
func (s Share) FolderName() string {
	f := s.Folder
	if f == "" {
		f = s.Remote
	}
	return strings.NewReplacer("/", "_", "\\", "_", "=", "_").Replace(f)
}

// Target is the rclone path the share points at, e.g. "gdrive:Photos".
func (s Share) Target() string {
	return s.Remote + ":" + strings.TrimPrefix(s.Path, "/")
}

// Client is a set of credentials that can log in to every enabled protocol
// and sees its Shares.
type Client struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Username string    `json:"username"`
	Password string    `json:"password"`
	Shares   []Share   `json:"shares"`
	Disabled bool      `json:"disabled,omitempty"`
	Created  time.Time `json:"created"`
}

// HasRemote reports whether the client shares the named remote.
func (c *Client) HasRemote(name string) bool {
	for _, s := range c.Shares {
		if s.Remote == name {
			return true
		}
	}
	return false
}

// RemoteNames lists the remotes shared with the client.
func (c *Client) RemoteNames() []string {
	var out []string
	for _, s := range c.Shares {
		out = append(out, s.Remote)
	}
	return out
}

// Fingerprint changes whenever anything affecting authentication or what
// the client can see changes.
func (c *Client) Fingerprint() string {
	b, _ := json.Marshal(struct {
		P string
		S []Share
		D bool
	}{c.Password, c.Shares, c.Disabled})
	return string(b)
}

// State is the whole persistent configuration.
type State struct {
	Version      int                          `json:"version"`
	RcloneBinary string                       `json:"rclone_binary"`
	RcloneConfig string                       `json:"rclone_config"`
	BindAddress  string                       `json:"bind_address"`
	PublicHost   string                       `json:"public_host,omitempty"`
	VFSCacheMode string                       `json:"vfs_cache_mode"`
	TLSCert      string                       `json:"tls_cert,omitempty"`
	TLSKey       string                       `json:"tls_key,omitempty"`
	Protocols    map[Protocol]*ProtocolConfig `json:"protocols"`
	Clients      []*Client                    `json:"clients"`
}

// VFSCacheModes rclone accepts for --vfs-cache-mode.
var VFSCacheModes = []string{"off", "minimal", "writes", "full"}

// Default returns a fresh state with sensible defaults.
func Default() *State {
	s := &State{Version: 1}
	s.fill()
	return s
}

// fill sets defaults on anything missing.
func (s *State) fill() {
	if s.Version == 0 {
		s.Version = 1
	}
	if s.BindAddress == "" {
		s.BindAddress = "0.0.0.0"
	}
	if s.VFSCacheMode == "" {
		s.VFSCacheMode = "writes"
	}
	if s.Protocols == nil {
		s.Protocols = map[Protocol]*ProtocolConfig{}
	}
	for _, p := range AllProtocols {
		if s.Protocols[p] == nil {
			s.Protocols[p] = &ProtocolConfig{
				Enabled: p == WebDAV || p == SFTP || p == S3,
				Port:    p.DefaultPort(),
			}
		}
		if s.Protocols[p].Port == 0 {
			s.Protocols[p].Port = p.DefaultPort()
		}
	}
}

// TLS reports whether TLS is configured.
func (s *State) TLS() bool {
	return s.TLSCert != "" && s.TLSKey != ""
}

// ClientByUsername returns the client with the given username or nil.
func (s *State) ClientByUsername(user string) *Client {
	for _, c := range s.Clients {
		if c.Username == user {
			return c
		}
	}
	return nil
}

// ClientByID returns the client with the given ID or nil.
func (s *State) ClientByID(id string) *Client {
	for _, c := range s.Clients {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// ClientsUsing returns the clients that share the given remote.
func (s *State) ClientsUsing(remote string) []*Client {
	var out []*Client
	for _, c := range s.Clients {
		if c.HasRemote(remote) {
			out = append(out, c)
		}
	}
	return out
}

// UniqueUsername derives a username from name that no other client uses.
// The client with ID except is ignored so renames keep their own name.
func (s *State) UniqueUsername(name, except string) string {
	base := Slug(name)
	if base == "" {
		base = "client"
	}
	cand := base
	for i := 2; ; i++ {
		c := s.ClientByUsername(cand)
		if c == nil || c.ID == except {
			return cand
		}
		cand = fmt.Sprintf("%s%d", base, i)
	}
}

// NextClientName suggests a name for a new client.
func (s *State) NextClientName() string {
	for i := len(s.Clients) + 1; ; i++ {
		n := fmt.Sprintf("client%d", i)
		if s.ClientByUsername(n) == nil {
			return n
		}
	}
}

// SortedClients returns clients sorted by name.
func (s *State) SortedClients() []*Client {
	out := append([]*Client(nil), s.Clients...)
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// Slug turns a free form name into a safe lowercase username.
func Slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case r == '-' || r == '_' || r == '.' || r == ' ':
			if b.Len() > 0 && !dash {
				b.WriteRune('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// Paths of the files kept in the state directory.
type Paths struct {
	Dir string
}

// File names inside the state directory.
func (p Paths) State() string      { return filepath.Join(p.Dir, "state.json") }
func (p Paths) Lock() string       { return filepath.Join(p.Dir, "state.lock") }
func (p Paths) Status() string     { return filepath.Join(p.Dir, "status.json") }
func (p Paths) Log() string        { return filepath.Join(p.Dir, "daemon.log") }
func (p Paths) PID() string        { return filepath.Join(p.Dir, "daemon.pid") }
func (p Paths) DaemonLock() string { return filepath.Join(p.Dir, "daemon.lock") }
func (p Paths) ClientsDir() string { return filepath.Join(p.Dir, "clients") }

// EnvDir overrides the state directory. The daemon sets it for the rclone
// processes so the auth proxy finds the state.
const EnvDir = "RCLONE_PROXY_TUI_DIR"

// DefaultDir is where state lives unless overridden.
func DefaultDir() string {
	if d := os.Getenv(EnvDir); d != "" {
		return d
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "rclone-proxy-tui")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "rclone-proxy-tui")
}

// Load reads the state, returning defaults if it doesn't exist yet.
func (p Paths) Load() (*State, error) {
	b, err := os.ReadFile(p.State())
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return nil, err
	}
	s := &State{}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p.State(), err)
	}
	s.fill()
	return s, nil
}

// Save writes the state atomically with private permissions since it holds
// client passwords.
func (p Paths) Save(s *State) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(p.State(), append(b, '\n'), 0o600)
}

// Update loads the state under an exclusive lock, applies fn and saves it.
func (p Paths) Update(fn func(*State) error) (*State, error) {
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(p.Lock(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck
	s, err := p.Load()
	if err != nil {
		return nil, err
	}
	if err := fn(s); err != nil {
		return nil, err
	}
	s.fill()
	return s, p.Save(s)
}

// WriteFileAtomic writes data to a temp file and renames it into place.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
