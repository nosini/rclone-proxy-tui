package daemon

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/nosini/rclone-proxy-tui/internal/state"
)

// ServeArgs builds the rclone command line serving proto. exe is this
// program, which rclone calls back as the auth proxy.
func ServeArgs(st *state.State, proto state.Protocol, exe string) ([]string, error) {
	pc := st.Protocols[proto]
	if pc == nil {
		return nil, fmt.Errorf("unknown protocol %q", proto)
	}
	if strings.ContainsAny(exe, " \t") {
		return nil, fmt.Errorf("the path to rclone-proxy-tui (%s) must not contain spaces, rclone splits the auth proxy command on whitespace", exe)
	}
	args := []string{"serve", string(proto)}
	if st.RcloneConfig != "" {
		args = append(args, "--config", st.RcloneConfig)
	}
	args = append(args,
		"--addr", net.JoinHostPort(st.BindAddress, strconv.Itoa(pc.Port)),
		"--auth-proxy", exe+" auth-proxy "+string(proto),
		"--vfs-cache-mode", st.VFSCacheMode,
	)
	if st.TLS() && proto.SupportsTLS() {
		args = append(args, "--cert", st.TLSCert, "--key", st.TLSKey)
	}
	extra, err := SplitArgs(pc.ExtraFlags)
	if err != nil {
		return nil, fmt.Errorf("%s extra flags: %w", proto.Title(), err)
	}
	return append(args, extra...), nil
}

// SplitArgs splits a command line into words, honouring single quotes,
// double quotes and backslash escapes.
func SplitArgs(s string) ([]string, error) {
	var (
		args  []string
		cur   strings.Builder
		in    bool
		quote rune
		esc   bool
	)
	for _, r := range s {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
			in = true
		case r == '\\' && quote != '\'':
			esc = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
			in = true
		case r == ' ' || r == '\t' || r == '\n':
			if in {
				args = append(args, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if esc {
		return nil, fmt.Errorf("trailing backslash")
	}
	if in {
		args = append(args, cur.String())
	}
	return args, nil
}
