// Package connect renders the instructions a client needs to connect:
// URLs per protocol and ready to paste rclone config sections.
package connect

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/nosini/rclone-proxy-tui/internal/state"
)

// Host returns the host name or address clients should use.
func Host(st *state.State) string {
	if st.PublicHost != "" {
		return st.PublicHost
	}
	if st.BindAddress != "" && st.BindAddress != "0.0.0.0" && st.BindAddress != "::" {
		return st.BindAddress
	}
	if ips := LocalIPs(); len(ips) > 0 {
		return ips[0]
	}
	return "localhost"
}

// LocalIPs lists this machine's non-loopback IPv4 addresses, private
// (LAN) addresses first.
func LocalIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var private, public []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP.To4()
			if ip == nil {
				continue
			}
			if ip.IsPrivate() {
				private = append(private, ip.String())
			} else if ip.IsGlobalUnicast() {
				public = append(public, ip.String())
			}
		}
	}
	sort.Strings(private)
	sort.Strings(public)
	return append(private, public...)
}

// Endpoint is how to reach one protocol.
type Endpoint struct {
	Protocol state.Protocol
	URL      string
	Notes    []string
	Rclone   string // rclone config section for the client side
}

func hostPort(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func remoteName(c *state.Client, p state.Protocol) string {
	return state.Slug("proxy-"+c.Username) + "-" + string(p)
}

// Endpoints lists how the client can connect to every enabled protocol.
func Endpoints(st *state.State, c *state.Client) []Endpoint {
	host := Host(st)
	scheme := "http"
	if st.TLS() {
		scheme = "https"
	}
	obscured := state.Obscure(c.Password)
	var eps []Endpoint
	for _, p := range state.AllProtocols {
		pc := st.Protocols[p]
		if pc == nil || !pc.Enabled {
			continue
		}
		hp := hostPort(host, pc.Port)
		name := remoteName(c, p)
		var ep Endpoint
		ep.Protocol = p
		switch p {
		case state.WebDAV:
			ep.URL = fmt.Sprintf("%s://%s/", scheme, hp)
			ep.Notes = []string{
				"Windows: Map network drive → " + ep.URL,
				"macOS Finder: Go → Connect to Server → " + ep.URL,
			}
			ep.Rclone = fmt.Sprintf("[%s]\ntype = webdav\nurl = %s\nvendor = rclone\nuser = %s\npass = %s\n", name, ep.URL, c.Username, obscured)
		case state.SFTP:
			ep.URL = fmt.Sprintf("sftp://%s@%s/", c.Username, hp)
			ep.Notes = []string{
				fmt.Sprintf("sftp -P %d %s@%s", pc.Port, c.Username, host),
				fmt.Sprintf("sshfs -p %d %s@%s:/ /mnt/point", pc.Port, c.Username, host),
			}
			ep.Rclone = fmt.Sprintf("[%s]\ntype = sftp\nhost = %s\nport = %d\nuser = %s\npass = %s\nshell_type = none\n", name, host, pc.Port, c.Username, obscured)
		case state.S3:
			ep.URL = fmt.Sprintf("%s://%s", scheme, hp)
			ep.Notes = []string{
				"Access key ID: " + c.Username,
				"Secret access key: " + c.Password,
				"Use path-style addressing; each shared remote is a bucket.",
			}
			ep.Rclone = fmt.Sprintf("[%s]\ntype = s3\nprovider = Rclone\nendpoint = %s\naccess_key_id = %s\nsecret_access_key = %s\n", name, ep.URL, c.Username, c.Password)
		case state.FTP:
			ep.URL = fmt.Sprintf("ftp://%s@%s/", c.Username, hp)
			tls := ""
			if st.TLS() {
				tls = "explicit_tls = true\n"
				ep.Notes = []string{"Use explicit FTPS (FTP over TLS)."}
			} else {
				ep.Notes = []string{"Plain FTP is unencrypted; only use it on a trusted network."}
			}
			ep.Rclone = fmt.Sprintf("[%s]\ntype = ftp\nhost = %s\nport = %d\nuser = %s\npass = %s\n%s", name, host, pc.Port, c.Username, obscured, tls)
		case state.HTTP:
			ep.URL = fmt.Sprintf("%s://%s/", scheme, hp)
			ep.Notes = []string{"Read-only. Open in a web browser and log in."}
		}
		eps = append(eps, ep)
	}
	return eps
}

// Text renders full connection instructions for a client.
func Text(st *state.State, c *state.Client) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Client:    %s\n", c.Name)
	fmt.Fprintf(&b, "Username:  %s\n", c.Username)
	fmt.Fprintf(&b, "Password:  %s\n", c.Password)
	var folders []string
	for _, s := range c.Shares {
		f := s.FolderName()
		if f != s.Target() && f+":" != s.Target() {
			f += " (" + s.Target() + ")"
		}
		folders = append(folders, f)
	}
	if len(folders) == 0 {
		folders = []string{"(none - this client cannot log in until a remote is added)"}
	}
	fmt.Fprintf(&b, "Folders:   %s\n", strings.Join(folders, ", "))
	if c.Disabled {
		b.WriteString("\n!! This client is disabled and cannot log in.\n")
	}
	eps := Endpoints(st, c)
	if len(eps) == 0 {
		b.WriteString("\nNo protocols are enabled. Enable some in the Server tab.\n")
		return b.String()
	}
	for _, ep := range eps {
		fmt.Fprintf(&b, "\n── %s ──\n%s\n", ep.Protocol.Title(), ep.URL)
		for _, n := range ep.Notes {
			fmt.Fprintf(&b, "  %s\n", n)
		}
	}
	b.WriteString("\n── rclone config for the client machine (append to its rclone.conf) ──\n\n")
	for _, ep := range eps {
		if ep.Rclone != "" {
			b.WriteString(ep.Rclone + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}
