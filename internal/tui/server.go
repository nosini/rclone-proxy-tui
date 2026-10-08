package tui

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nosini/rclone-proxy-tui/internal/connect"
	"github.com/nosini/rclone-proxy-tui/internal/daemon"
	"github.com/nosini/rclone-proxy-tui/internal/service"
	"github.com/nosini/rclone-proxy-tui/internal/state"
)

type serverRow struct {
	label string
	value string
	proto state.Protocol
	enter func(m *Model) tea.Cmd
}

func (m *Model) serverRows() []serverRow {
	st := m.st
	var rows []serverRow

	daemonVal := sRed.Render("○ stopped") + sMuted.Render("  enter to start")
	if m.daemonUp {
		daemonVal = sGreen.Render(fmt.Sprintf("● running (pid %d)", m.status.PID)) + sMuted.Render("  enter to stop")
	}
	rows = append(rows, serverRow{label: "Daemon", value: daemonVal, enter: func(m *Model) tea.Cmd {
		svc := m.svc
		if m.daemonUp {
			return m.async("Stopping daemon…", func() doneMsg {
				if err := svc.Stop(); err != nil {
					return doneMsg{err: err}
				}
				return doneMsg{text: "Daemon stopped - clients can't connect until it is started again"}
			})
		}
		return m.async("Starting daemon…", func() doneMsg {
			if err := svc.Start(); err != nil {
				return doneMsg{err: err}
			}
			return doneMsg{text: "Daemon started (" + svc.Mode() + ")"}
		})
	}})

	bootVal := sYellow.Render("no") + sMuted.Render("  enter to install a systemd service (survives logout and reboot)")
	if !service.HasSystemd() {
		bootVal = sMuted.Render("systemd not available - run `rclone-proxy-tui daemon` from your init system")
	} else if service.Installed() {
		bootVal = sGreen.Render("yes") + sMuted.Render(" ("+m.svc.Mode()+")  enter to remove")
	}
	rows = append(rows, serverRow{label: "Start on boot", value: bootVal, enter: func(m *Model) tea.Cmd {
		if !service.HasSystemd() {
			return nil
		}
		svc := m.svc
		if service.Installed() {
			m.push(&confirmModal{title: "Remove service", yes: "remove",
				text: "Stop the daemon and remove the systemd service? Clients will not be able to connect until you start it again.",
				onYes: func(m *Model) tea.Cmd {
					return m.async("Removing service…", func() doneMsg {
						if err := svc.Uninstall(); err != nil {
							return doneMsg{err: err}
						}
						return doneMsg{text: "Service removed"}
					})
				}})
			return nil
		}
		return m.async("Installing systemd service…", func() doneMsg {
			notes, err := svc.Install()
			if err != nil {
				return doneMsg{err: err}
			}
			return doneMsg{then: func(m *Model) tea.Cmd {
				m.push(newInfoModal("Service installed", notes+"\n\nUnit file: "+service.UnitPath()+"\n\n"+svc.Unit()))
				return nil
			}}
		})
	}})

	rows = append(rows, serverRow{label: "Listen address", value: st.BindAddress + sMuted.Render("  0.0.0.0 = all interfaces, 127.0.0.1 = this machine only"), enter: func(m *Model) tea.Cmd {
		m.push(newInputModal("Listen address", "IP address the servers listen on. 0.0.0.0 listens everywhere; use a specific address (e.g. a VPN/Tailscale IP) to limit exposure.", m.st.BindAddress,
			func(m *Model, v string) (tea.Cmd, error) {
				if v == "" {
					v = "0.0.0.0"
				}
				if net.ParseIP(v) == nil {
					return nil, fmt.Errorf("not an IP address")
				}
				return nil, m.save(func(s *state.State) error { s.BindAddress = v; return nil })
			}))
		return nil
	}})

	host := connect.Host(st)
	hostVal := host + sMuted.Render("  (auto-detected)")
	if st.PublicHost != "" {
		hostVal = st.PublicHost
	}
	rows = append(rows, serverRow{label: "Address for clients", value: hostVal, enter: func(m *Model) tea.Cmd {
		prompt := "Host name or IP shown in client connection info (e.g. a DNS name, public IP or Tailscale name). Leave empty to auto-detect."
		if ips := connect.LocalIPs(); len(ips) > 0 {
			prompt += "\n\nThis machine's addresses: " + strings.Join(ips, ", ")
		}
		m.push(newInputModal("Address for clients", prompt, m.st.PublicHost, func(m *Model, v string) (tea.Cmd, error) {
			return nil, m.save(func(s *state.State) error { s.PublicHost = v; return nil })
		}))
		return nil
	}})

	rows = append(rows, serverRow{label: "VFS cache mode", value: st.VFSCacheMode + sMuted.Render("  enter to cycle; 'writes' is needed for most WebDAV/SFTP/FTP clients"), enter: func(m *Model) tea.Cmd {
		_ = m.save(func(s *state.State) error {
			for i, v := range state.VFSCacheModes {
				if v == s.VFSCacheMode {
					s.VFSCacheMode = state.VFSCacheModes[(i+1)%len(state.VFSCacheModes)]
					return nil
				}
			}
			s.VFSCacheMode = "writes"
			return nil
		})
		return nil
	}})

	fileRow := func(label, val, help string, set func(s *state.State, v string)) serverRow {
		shown := val
		if shown == "" {
			shown = sMuted.Render("(none)")
		}
		return serverRow{label: label, value: shown, enter: func(m *Model) tea.Cmd {
			m.push(newInputModal(label, help, val, func(m *Model, v string) (tea.Cmd, error) {
				if v != "" {
					if _, err := os.Stat(v); err != nil {
						return nil, err
					}
				}
				return nil, m.save(func(s *state.State) error { set(s, v); return nil })
			}))
			return nil
		}}
	}
	tlsHelp := " Set both a certificate and a key to serve WebDAV, S3, HTTP and FTP over TLS (https/ftps). SFTP is always encrypted."
	rows = append(rows, fileRow("TLS certificate", st.TLSCert, "Path to a PEM certificate (chain)."+tlsHelp, func(s *state.State, v string) { s.TLSCert = v }))
	rows = append(rows, fileRow("TLS key", st.TLSKey, "Path to the PEM private key."+tlsHelp, func(s *state.State, v string) { s.TLSKey = v }))

	ver := ""
	if m.status != nil && m.status.RcloneVersion != "" {
		ver = sMuted.Render("  " + m.status.RcloneVersion)
	}
	binVal := st.RcloneBinary
	if binVal == "" {
		binVal = sRed.Render("not found")
	}
	rows = append(rows, fileRow("rclone binary", st.RcloneBinary, "Path to the rclone executable.", func(s *state.State, v string) { s.RcloneBinary = v }))
	rows[len(rows)-1].value = binVal + ver
	rows = append(rows, fileRow("rclone config", st.RcloneConfig, "Path to rclone.conf. The remotes in it are what you can share.", func(s *state.State, v string) { s.RcloneConfig = v }))

	for _, p := range state.AllProtocols {
		rows = append(rows, serverRow{proto: p})
	}
	return rows
}

func (m *Model) serverKey(k tea.KeyMsg) tea.Cmd {
	rows := m.serverRows()
	if moveCursor(&m.cursor[tabServer], k.String(), len(rows), m.bodyHeight()-2) {
		return nil
	}
	i := m.cursor[tabServer]
	if i >= len(rows) {
		return nil
	}
	row := rows[i]
	switch k.String() {
	case "R":
		svc := m.svc
		return m.async("Restarting daemon…", func() doneMsg {
			if err := svc.Restart(); err != nil {
				return doneMsg{err: err}
			}
			return doneMsg{text: "Daemon restarted"}
		})
	case "l":
		lines, err := daemon.ScanLog(m.paths.Log(), 300)
		if err != nil {
			m.setFlash("No log yet: "+err.Error(), true)
			return nil
		}
		im := newInfoModal("Daemon log ("+m.paths.Log()+")", strings.Join(lines, "\n"))
		im.layout(m)
		im.vp.GotoBottom()
		m.push(im)
		return nil
	}
	if row.proto != "" {
		return m.protoKey(row.proto, k)
	}
	if k.String() == "enter" && row.enter != nil {
		return row.enter(m)
	}
	return nil
}

func (m *Model) protoKey(p state.Protocol, k tea.KeyMsg) tea.Cmd {
	pc := m.st.Protocols[p]
	switch k.String() {
	case " ", "x":
		_ = m.save(func(s *state.State) error {
			s.Protocols[p].Enabled = !s.Protocols[p].Enabled
			return nil
		})
		if m.st.Protocols[p].Enabled {
			m.setFlash(p.Title()+" enabled on port "+strconv.Itoa(pc.Port), false)
			return m.ensureDaemon()
		}
		m.setFlash(p.Title()+" disabled", false)
	case "enter":
		m.push(newInputModal(p.Title()+" port", "TCP port for "+p.Title()+" (all clients share it; open it in your firewall).", strconv.Itoa(pc.Port),
			func(m *Model, v string) (tea.Cmd, error) {
				port, err := strconv.Atoi(v)
				if err != nil || port < 1 || port > 65535 {
					return nil, fmt.Errorf("enter a port between 1 and 65535")
				}
				for _, other := range state.AllProtocols {
					if other != p && m.st.Protocols[other].Port == port {
						return nil, fmt.Errorf("port %d is already used by %s", port, other.Title())
					}
				}
				return nil, m.save(func(s *state.State) error { s.Protocols[p].Port = port; return nil })
			}))
	case "f":
		m.push(newInputModal(p.Title()+" extra flags",
			"Extra flags for `rclone serve "+string(p)+"`, e.g. --read-only, --vfs-cache-max-size 10G, --dir-cache-time 1m, -v",
			pc.ExtraFlags, func(m *Model, v string) (tea.Cmd, error) {
				if _, err := daemon.SplitArgs(v); err != nil {
					return nil, err
				}
				return nil, m.save(func(s *state.State) error { s.Protocols[p].ExtraFlags = v; return nil })
			}))
	}
	return nil
}

func (m *Model) serverView(h int) (string, string) {
	footer := wrapHints(m.w, "enter", "change / start-stop", "space", "toggle protocol", "f", "extra flags",
		"R", "restart daemon", "l", "log", "?", "help", "q", "quit")
	rows := m.serverRows()
	var lines []string
	cur := m.cursor[tabServer]
	labelW := 20
	protoHeader := false
	for i, r := range rows {
		var line string
		if r.proto != "" {
			if !protoHeader {
				lines = append(lines, "", sHeader.Render("    PROTOCOL  PORT   STATUS"))
				protoHeader = true
			}
			pc := m.st.Protocols[r.proto]
			var ps *state.ProtoStatus
			if m.status != nil {
				ps = m.status.Protocols[r.proto]
			}
			box := "[ ]"
			if pc.Enabled {
				box = sGreen.Render("[x]")
			}
			extra := ""
			if pc.ExtraFlags != "" {
				extra = sMuted.Render("  flags: " + pc.ExtraFlags)
			}
			status := stateText(ps, pc.Enabled, m.daemonUp)
			desc := sMuted.Render("  " + r.proto.Description())
			line = fmt.Sprintf(" %s %s %s %s", box, pad(r.proto.Title(), 8), pad(strconv.Itoa(pc.Port), 6), status) + desc + extra
		} else {
			line = "  " + pad(r.label, labelW) + " " + r.value
		}
		if i == cur {
			line = sCursor.Render(pad(line, m.w))
		} else {
			line = trunc(line, m.w)
		}
		lines = append(lines, line)
	}
	// Show the full error of the protocol under the cursor.
	if cur < len(rows) && rows[cur].proto != "" && m.status != nil {
		if ps := m.status.Protocols[rows[cur].proto]; ps != nil && ps.Error != "" && m.st.Protocols[rows[cur].proto].Enabled {
			lines = append(lines, "", sErr.Render(rows[cur].proto.Title()+" error:"))
			for _, l := range strings.Split(ps.Error, "\n") {
				lines = append(lines, "  "+trunc(l, m.w-2))
			}
		}
	}
	start, end := window(cur, len(lines), h)
	if len(lines) <= h {
		start, end = 0, len(lines)
	}
	return strings.Join(lines[start:end], "\n"), footer
}
