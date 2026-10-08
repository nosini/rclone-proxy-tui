package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nosini/rclone-proxy-tui/internal/rclone"
	"github.com/nosini/rclone-proxy-tui/internal/state"
)

func (m *Model) currentRemote() *rclone.Remote {
	i := m.cursor[tabRemotes]
	if i < 0 || i >= len(m.remotes) {
		return nil
	}
	return &m.remotes[i]
}

// selectedOrCurrent returns the selected remote names, or the one under the
// cursor if nothing is selected.
func (m *Model) selectedOrCurrent() []string {
	var names []string
	for _, r := range m.remotes {
		if m.selected[r.Name] {
			names = append(names, r.Name)
		}
	}
	if len(names) == 0 {
		if r := m.currentRemote(); r != nil {
			names = []string{r.Name}
		}
	}
	return names
}

func (m *Model) remotesKey(k tea.KeyMsg) tea.Cmd {
	n := len(m.remotes)
	if moveCursor(&m.cursor[tabRemotes], k.String(), n, m.bodyHeight()-2) {
		return nil
	}
	cur := m.currentRemote()
	switch k.String() {
	case " ", "space":
		if cur != nil {
			m.selected[cur.Name] = !m.selected[cur.Name]
			if !m.selected[cur.Name] {
				delete(m.selected, cur.Name)
			}
			if m.cursor[tabRemotes] < n-1 {
				m.cursor[tabRemotes]++
			}
		}
	case "esc":
		m.selected = map[string]bool{}
	case "ctrl+a", "*":
		if len(m.selected) == n {
			m.selected = map[string]bool{}
		} else {
			for _, r := range m.remotes {
				m.selected[r.Name] = true
			}
		}
	case "s", "enter":
		return m.shareFlow(m.selectedOrCurrent())
	case "a":
		return m.addToClientFlow(m.selectedOrCurrent())
	case "n":
		return m.newRemoteFlow()
	case "e":
		if cur != nil {
			return m.editRemoteFlow(*cur)
		}
	case "d", "delete":
		if cur != nil {
			m.deleteRemoteFlow(*cur)
		}
	case "t":
		if cur != nil {
			rc, name := *m.rc, cur.Name
			return m.async("Testing "+name+"…", func() doneMsg {
				out, err := rc.Test(name)
				if err != nil {
					return doneMsg{err: fmt.Errorf("%s: %v", name, err)}
				}
				return doneMsg{then: func(m *Model) tea.Cmd {
					m.push(newInfoModal("Test "+name, out))
					return nil
				}}
			})
		}
	case "o":
		if cur != nil {
			return m.reconnectCmd(cur.Name)
		}
	case "c":
		return m.rcloneConfigCmd()
	case "r":
		m.busy = "Loading remotes…"
		return tea.Sequence(m.loadRemotes(), func() tea.Msg { return doneMsg{} })
	}
	return nil
}

func (m *Model) rcloneConfigCmd() tea.Cmd {
	if m.rc.Bin == "" {
		m.setFlash(rclone.ErrNotFound.Error(), true)
		return nil
	}
	return tea.ExecProcess(m.rc.Command(nil, "config"), func(err error) tea.Msg {
		return doneMsg{err: err, reload: true, text: "Back from rclone config"}
	})
}

func (m *Model) reconnectCmd(name string) tea.Cmd {
	cmd := m.rc.Command(nil, "config", "reconnect", name+":")
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return doneMsg{err: fmt.Errorf("rclone config reconnect %s: %w", name, err), reload: true}
		}
		return doneMsg{text: name + " is set up", reload: true}
	})
}

// shareFlow creates a new client for the given remotes after asking for a
// name: space, s, enter.
func (m *Model) shareFlow(names []string) tea.Cmd {
	if len(names) == 0 {
		m.setFlash("No remotes yet - press n to add one", true)
		return nil
	}
	prompt := "Create a client login that can access: " + strings.Join(names, ", ") +
		"\n\nA username and password are generated for you. Name:"
	m.push(newInputModal("Share with a new client", prompt, m.st.NextClientName(), func(m *Model, value string) (tea.Cmd, error) {
		if value == "" {
			return nil, fmt.Errorf("enter a name")
		}
		c, err := m.createClient(value, "", sharesFor(names))
		if err != nil {
			return nil, err
		}
		m.selected = map[string]bool{}
		m.showClient(c.ID)
		return m.ensureDaemon(), nil
	}))
	return nil
}

func sharesFor(names []string) []state.Share {
	var shares []state.Share
	for _, n := range names {
		shares = append(shares, state.Share{Remote: n})
	}
	return shares
}

func (m *Model) createClient(name, username string, shares []state.Share) (*state.Client, error) {
	c := &state.Client{
		ID:       state.NewID(),
		Name:     name,
		Password: state.GeneratePassword(20),
		Shares:   shares,
		Created:  time.Now(),
	}
	err := m.save(func(s *state.State) error {
		if username == "" {
			c.Username = s.UniqueUsername(name, "")
		} else if other := s.ClientByUsername(username); other != nil {
			return fmt.Errorf("username %q is already used by %s", username, other.Name)
		} else {
			c.Username = username
		}
		s.Clients = append(s.Clients, c)
		return nil
	})
	return c, err
}

func (m *Model) addToClientFlow(names []string) tea.Cmd {
	if len(names) == 0 {
		m.setFlash("No remotes yet - press n to add one", true)
		return nil
	}
	if len(m.st.Clients) == 0 {
		return m.shareFlow(names)
	}
	var items []pickItem
	for _, c := range m.st.SortedClients() {
		items = append(items, pickItem{label: c.Name, detail: strings.Join(c.RemoteNames(), ", "), value: c.ID})
	}
	m.push(&pickModal{
		title:  "Add to existing client",
		prompt: "Give an existing client access to: " + strings.Join(names, ", "),
		items:  items,
		onPick: func(m *Model, it pickItem) tea.Cmd {
			var added []string
			err := m.save(func(s *state.State) error {
				c := s.ClientByID(it.value)
				if c == nil {
					return fmt.Errorf("client no longer exists")
				}
				for _, n := range names {
					if !c.HasRemote(n) {
						c.Shares = append(c.Shares, state.Share{Remote: n})
						added = append(added, n)
					}
				}
				return nil
			})
			if err != nil {
				return nil
			}
			m.selected = map[string]bool{}
			if len(added) == 0 {
				m.setFlash(it.label+" already has those remotes", false)
			} else {
				m.setFlash(fmt.Sprintf("%s can now access %s", it.label, strings.Join(added, ", ")), false)
			}
			return nil
		},
	})
	return nil
}

func (m *Model) withBackends(fn func(m *Model) tea.Cmd) tea.Cmd {
	if m.backends != nil {
		return fn(m)
	}
	if m.rc.Bin == "" {
		m.setFlash(rclone.ErrNotFound.Error(), true)
		return nil
	}
	rc := *m.rc
	return m.async("Loading backend list from rclone…", func() doneMsg {
		b, err := rc.Backends()
		if err != nil {
			return doneMsg{err: err}
		}
		return doneMsg{then: func(m *Model) tea.Cmd {
			m.backends = b
			return fn(m)
		}}
	})
}

// popular backends float to the top of the type picker.
var popularBackends = []string{"crypt", "drive", "onedrive", "s3", "sftp", "dropbox", "b2", "webdav", "local", "alias", "union", "smb", "ftp", "box", "pcloud", "mega"}

func (m *Model) newRemoteFlow() tea.Cmd {
	return m.withBackends(func(m *Model) tea.Cmd {
		rank := map[string]int{}
		for i, n := range popularBackends {
			rank[n] = i + 1
		}
		bs := append([]rclone.Backend(nil), m.backends...)
		sort.SliceStable(bs, func(i, j int) bool {
			ri, rj := rank[bs[i].Name], rank[bs[j].Name]
			if ri == 0 {
				ri = 1000
			}
			if rj == 0 {
				rj = 1000
			}
			return ri < rj
		})
		var items []pickItem
		for _, b := range bs {
			items = append(items, pickItem{label: b.Name, detail: b.Description, value: b.Name})
		}
		m.push(&pickModal{
			title:  "New remote: choose a type",
			prompt: "Pick the storage system. Use crypt to add an encrypted layer on top of another remote.",
			items:  items,
			onPick: func(m *Model, it pickItem) tea.Cmd {
				for _, b := range m.backends {
					if b.Name == it.value {
						m.push(newRemoteForm(m, b, nil))
					}
				}
				return nil
			},
		})
		return nil
	})
}

func (m *Model) editRemoteFlow(r rclone.Remote) tea.Cmd {
	return m.withBackends(func(m *Model) tea.Cmd {
		for _, b := range m.backends {
			if b.Name == r.Type {
				m.push(newRemoteForm(m, b, &r))
				return nil
			}
		}
		m.setFlash("Unknown backend type "+r.Type+" - opening rclone config", true)
		return m.rcloneConfigCmd()
	})
}

// dependents lists remotes that wrap the named remote.
func (m *Model) dependents(name string) []string {
	var out []string
	for _, r := range m.remotes {
		for _, key := range []string{"remote", "upstreams"} {
			v := r.Config[key]
			for _, part := range strings.Fields(v) {
				part = strings.Trim(part, `"`)
				if i := strings.Index(part, "="); i >= 0 && r.Type == "combine" {
					part = part[i+1:]
				}
				if strings.HasPrefix(part, name+":") {
					out = append(out, r.Name)
				}
			}
		}
	}
	return out
}

func (m *Model) deleteRemoteFlow(r rclone.Remote) {
	text := fmt.Sprintf("Delete remote %q (%s) from rclone.conf?", r.Name, r.Type)
	if users := m.st.ClientsUsing(r.Name); len(users) > 0 {
		var names []string
		for _, c := range users {
			names = append(names, c.Name)
		}
		text += "\n\nIt is shared with: " + strings.Join(names, ", ") + ". They will lose access to it."
	}
	if deps := m.dependents(r.Name); len(deps) > 0 {
		text += "\n\nWarning: " + strings.Join(deps, ", ") + " wrap this remote and will stop working."
	}
	if r.Type != "crypt" {
		text += "\n\nOnly the configuration is removed, not the files."
	} else {
		text += "\n\nMake sure you have the crypt password backed up if you still need the encrypted files."
	}
	m.push(&confirmModal{title: "Delete remote", text: text, yes: "delete", onYes: func(m *Model) tea.Cmd {
		rc, name := *m.rc, r.Name
		return m.async("Deleting "+name+"…", func() doneMsg {
			if err := rc.Delete(name); err != nil {
				return doneMsg{err: err}
			}
			return doneMsg{text: "Deleted " + name, reload: true, then: func(m *Model) tea.Cmd {
				_ = m.save(func(s *state.State) error {
					for _, c := range s.Clients {
						var keep []state.Share
						for _, sh := range c.Shares {
							if sh.Remote != name {
								keep = append(keep, sh)
							}
						}
						c.Shares = keep
					}
					return nil
				})
				delete(m.selected, name)
				return nil
			}}
		})
	}})
}

func (m *Model) remotesView(h int) (string, string) {
	footer := wrapHints(m.w,
		"space", "select", "s", "share → new client", "a", "add to client", "n", "new", "e", "edit",
		"d", "delete", "t", "test", "o", "oauth login", "c", "rclone config", "?", "help", "q", "quit")
	var b strings.Builder
	if m.rc.Bin == "" {
		return sErr.Render(rclone.ErrNotFound.Error()), footer
	}
	if !m.remotesLoaded {
		return sMuted.Render("Loading remotes…"), footer
	}
	if m.remotesErr != nil {
		return sErr.Render("Could not read remotes: "+m.remotesErr.Error()) + "\n\n" + sMuted.Render("Config file: "+m.rc.Config), footer
	}
	if len(m.remotes) == 0 {
		b.WriteString("\n  No remotes configured in " + m.rc.Config + "\n\n")
		b.WriteString("  " + hints("n", "add a remote here") + "\n")
		b.WriteString("  " + hints("c", "use rclone's interactive config instead") + "\n")
		return b.String(), footer
	}

	nameW, typeW, usedW := 22, 12, 22
	if m.w < 110 {
		nameW, typeW, usedW = 16, 8, 16
	}
	detailW := m.w - 4 - nameW - typeW - usedW - 4
	sel := ""
	if len(m.selected) > 0 {
		sel = fmt.Sprintf("  (%d selected - press s to share them, esc to clear)", len(m.selected))
	}
	b.WriteString(sHeader.Render(pad("     NAME", nameW+5)+" "+pad("TYPE", typeW)+" "+pad("SHARED WITH", usedW)+" "+"POINTS AT") + sYellow.Render(sel) + "\n")
	start, end := window(m.cursor[tabRemotes], len(m.remotes), h-1)
	for i := start; i < end; i++ {
		r := m.remotes[i]
		box := "[ ]"
		if m.selected[r.Name] {
			box = "[x]"
		}
		used := m.st.ClientsUsing(r.Name)
		usedText := "-"
		if len(used) > 0 {
			var names []string
			for _, c := range used {
				names = append(names, c.Name)
			}
			usedText = strings.Join(names, ", ")
		}
		line := " " + box + " " + pad(r.Name, nameW) + " " + pad(r.Type, typeW) + " " + pad(usedText, usedW) + " " + pad(r.Detail(), detailW)
		switch {
		case i == m.cursor[tabRemotes]:
			line = sCursor.Render(pad(line, m.w))
		case m.selected[r.Name]:
			line = sYellow.Render(line)
		}
		b.WriteString(line + "\n")
	}
	return b.String(), footer
}
