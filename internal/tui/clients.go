package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/nosini/rclone-proxy-tui/internal/connect"
	"github.com/nosini/rclone-proxy-tui/internal/state"
)

func (m *Model) currentClient() *state.Client {
	cs := m.st.SortedClients()
	i := m.cursor[tabClients]
	if i < 0 || i >= len(cs) {
		return nil
	}
	return cs[i]
}

func (m *Model) clientsKey(k tea.KeyMsg) tea.Cmd {
	n := len(m.st.Clients)
	if moveCursor(&m.cursor[tabClients], k.String(), n, m.bodyHeight()-2) {
		return nil
	}
	c := m.currentClient()
	switch k.String() {
	case "n":
		m.push(newClientForm(m, nil))
	case "enter", "v":
		if c != nil {
			m.showClient(c.ID)
		}
	case "e":
		if c != nil {
			m.push(newClientForm(m, c))
		}
	case "y":
		if c != nil {
			copyToClipboard(c.Password)
			m.setFlash("Copied "+c.Name+"'s password to the clipboard (if your terminal supports OSC 52)", false)
		}
	case "p":
		if c != nil {
			id, name := c.ID, c.Name
			m.push(&confirmModal{title: "New password", yes: "generate",
				text: "Generate a new password for " + name + "? The old one stops working after the servers finish draining uploads and restart.",
				onYes: func(m *Model) tea.Cmd {
					if m.save(func(s *state.State) error {
						if c := s.ClientByID(id); c != nil {
							c.Password = state.GeneratePassword(20)
						}
						return nil
					}) == nil {
						m.showClient(id)
					}
					return nil
				}})
		}
	case "x":
		if c != nil {
			id := c.ID
			_ = m.save(func(s *state.State) error {
				if c := s.ClientByID(id); c != nil {
					c.Disabled = !c.Disabled
					if c.Disabled {
						m.setFlash(c.Name+" disabled - access is being revoked after pending uploads drain", false)
					} else {
						m.setFlash(c.Name+" enabled", false)
					}
				}
				return nil
			})
		}
	case "d", "delete":
		if c != nil {
			id, name := c.ID, c.Name
			m.push(&confirmModal{title: "Delete client", yes: "delete",
				text: "Delete client " + name + "? Existing sessions are disconnected after pending uploads drain. The remotes themselves are not touched.",
				onYes: func(m *Model) tea.Cmd {
					_ = m.save(func(s *state.State) error {
						for i, c := range s.Clients {
							if c.ID == id {
								s.Clients = append(s.Clients[:i], s.Clients[i+1:]...)
								break
							}
						}
						return nil
					})
					m.setFlash("Deleted "+name, false)
					return nil
				}})
		}
	}
	return nil
}

// showClient opens the login and connection info for a client.
func (m *Model) showClient(id string) {
	c := m.st.ClientByID(id)
	if c == nil {
		return
	}
	text := connect.Text(m.st, c)
	if !m.daemonUp {
		text = "Note: the server daemon is not running yet. It is started automatically,\nor use the Server tab.\n\n" + text
	}
	if ips := connect.LocalIPs(); m.st.PublicHost == "" && len(ips) > 1 {
		text += "\nThis machine has several addresses (" + strings.Join(ips, ", ") + ").\nSet the address shown here in the Server tab if the one above is wrong.\n"
	}
	m.push(newInfoModal("Client "+c.Name, text,
		infoKey{"y", "copy password", func(m *Model) tea.Cmd {
			copyToClipboard(c.Password)
			m.setFlash("Copied password to the clipboard (if your terminal supports OSC 52)", false)
			return nil
		}},
		infoKey{"w", "save to file", func(m *Model) tea.Cmd {
			path := filepath.Join(m.paths.ClientsDir(), c.Username+".txt")
			if err := state.WriteFileAtomic(path, []byte(connect.Text(m.st, c)), 0o600); err != nil {
				m.setFlash(err.Error(), true)
			} else {
				m.setFlash("Saved to "+path, false)
			}
			return nil
		}},
	))
}

func (m *Model) clientsView(h int) (string, string) {
	footer := wrapHints(m.w,
		"enter", "login & URLs", "n", "new", "e", "edit remotes", "y", "copy password", "p", "new password",
		"x", "enable/disable", "d", "delete", "?", "help", "q", "quit")
	cs := m.st.SortedClients()
	var b strings.Builder
	if len(cs) == 0 {
		b.WriteString("\n  No clients yet.\n\n")
		b.WriteString("  The quickest way: go to the Remotes tab (1), press space on the remotes\n")
		b.WriteString("  you want to share, then s and enter. Or press n here.\n")
		return b.String(), footer
	}
	nameW, userW := 22, 18
	if m.w < 100 {
		nameW, userW = 16, 12
	}
	remW := m.w - 6 - nameW - userW - 3
	b.WriteString(sHeader.Render("   "+pad("NAME", nameW)+" "+pad("USERNAME", userW)+" "+"REMOTES (each is a folder for the client)") + "\n")
	start, end := window(m.cursor[tabClients], len(cs), h-1)
	for i := start; i < end; i++ {
		c := cs[i]
		mark := sGreen.Render("●")
		if c.Disabled {
			mark = sMuted.Render("○")
		}
		var rem []string
		for _, s := range c.Shares {
			t := s.Remote
			if s.Path != "" {
				t += ":" + s.Path
			}
			if m.remotesLoaded && m.remoteByName(s.Remote) == nil {
				t += " (missing!)"
			}
			rem = append(rem, t)
		}
		remText := strings.Join(rem, ", ")
		if remText == "" {
			remText = "(none - press e to add)"
		}
		name := c.Name
		if c.Disabled {
			name += " (disabled)"
		}
		line := " " + pad(name, nameW) + " " + pad(c.Username, userW) + " " + pad(remText, remW)
		if i == m.cursor[tabClients] {
			b.WriteString(" " + mark + sCursor.Render(pad(line, m.w-2)) + "\n")
		} else {
			b.WriteString(" " + mark + line + "\n")
		}
	}
	return b.String(), footer
}

// ---- client form: name, username and a checklist of remotes ----

type clientItem struct {
	remote  string
	detail  string
	checked bool
	path    string
	missing bool
}

type clientForm struct {
	id          string // empty for a new client
	name        textinput.Model
	user        textinput.Model
	userTouched bool
	items       []*clientItem
	focus       int // 0 name, 1 username, 2.. items, last = save
	err         string
}

func newClientForm(m *Model, c *state.Client) *clientForm {
	f := &clientForm{}
	name, user := m.st.NextClientName(), ""
	if c != nil {
		f.id, name, user = c.ID, c.Name, c.Username
		f.userTouched = true
	}
	f.name = newInput(name, "e.g. laptop", 40)
	f.user = newInput(user, "generated from the name", 40)
	if c == nil {
		f.user.SetValue(m.st.UniqueUsername(name, ""))
	}
	shares := map[string]state.Share{}
	if c != nil {
		for _, s := range c.Shares {
			shares[s.Remote] = s
		}
	}
	seen := map[string]bool{}
	for _, r := range m.remotes {
		s, ok := shares[r.Name]
		f.items = append(f.items, &clientItem{remote: r.Name, detail: r.Type + " " + r.Detail(), checked: ok, path: s.Path})
		seen[r.Name] = true
	}
	if c != nil {
		for _, s := range c.Shares {
			if !seen[s.Remote] {
				f.items = append(f.items, &clientItem{remote: s.Remote, detail: "not in rclone.conf", checked: true, path: s.Path, missing: true})
			}
		}
	}
	f.setFocus(0)
	return f
}

func (f *clientForm) count() int { return 2 + len(f.items) + 1 }

func (f *clientForm) setFocus(i int) {
	n := f.count()
	f.focus = (i%n + n) % n
	f.name.Blur()
	f.user.Blur()
	switch f.focus {
	case 0:
		f.name.Focus()
	case 1:
		f.user.Focus()
	}
}

func (f *clientForm) item() *clientItem {
	i := f.focus - 2
	if i >= 0 && i < len(f.items) {
		return f.items[i]
	}
	return nil
}

func (f *clientForm) Update(m *Model, k tea.KeyMsg) (bool, tea.Cmd) {
	switch k.String() {
	case "esc":
		return true, nil
	case "tab", "down":
		f.setFocus(f.focus + 1)
		return false, nil
	case "shift+tab", "up":
		f.setFocus(f.focus - 1)
		return false, nil
	case "ctrl+s":
		return f.save(m)
	case "enter":
		if f.focus == f.count()-1 {
			return f.save(m)
		}
		if it := f.item(); it != nil {
			it.checked = !it.checked
			return false, nil
		}
		f.setFocus(f.focus + 1)
		return false, nil
	}
	if it := f.item(); it != nil {
		switch k.String() {
		case " ", "x":
			it.checked = !it.checked
		case "p", "/":
			m.push(newInputModal("Sub-folder of "+it.remote,
				"Only share this folder inside "+it.remote+": (leave empty for the whole remote)", it.path,
				func(m *Model, v string) (tea.Cmd, error) {
					it.path = strings.Trim(v, "/")
					if it.path != "" {
						it.checked = true
					}
					return nil, nil
				}))
		}
		return false, nil
	}
	var cmd tea.Cmd
	switch f.focus {
	case 0:
		f.name, cmd = f.name.Update(k)
		if !f.userTouched {
			f.user.SetValue(m.st.UniqueUsername(f.name.Value(), f.id))
		}
	case 1:
		f.user, cmd = f.user.Update(k)
		f.userTouched = true
	}
	f.err = ""
	return false, cmd
}

func (f *clientForm) save(m *Model) (bool, tea.Cmd) {
	name := strings.TrimSpace(f.name.Value())
	user := strings.TrimSpace(f.user.Value())
	if name == "" {
		f.err = "enter a name"
		return false, nil
	}
	if user == "" {
		user = m.st.UniqueUsername(name, f.id)
	}
	if strings.ContainsAny(user, " :/\\\"'") {
		f.err = "the username must not contain spaces, quotes, : or slashes"
		return false, nil
	}
	var shares []state.Share
	for _, it := range f.items {
		if it.checked {
			shares = append(shares, state.Share{Remote: it.remote, Path: it.path})
		}
	}
	if f.id == "" {
		c, err := m.createClient(name, user, shares)
		if err != nil {
			f.err = err.Error()
			return false, nil
		}
		m.showClient(c.ID)
		return true, m.ensureDaemon()
	}
	id := f.id
	err := m.save(func(s *state.State) error {
		c := s.ClientByID(id)
		if c == nil {
			return fmt.Errorf("client no longer exists")
		}
		if other := s.ClientByUsername(user); other != nil && other.ID != id {
			return fmt.Errorf("username %q is already used by %s", user, other.Name)
		}
		c.Name, c.Username, c.Shares = name, user, shares
		return nil
	})
	if err != nil {
		f.err = err.Error()
		return false, nil
	}
	m.setFlash("Saved "+name, false)
	return true, nil
}

func (f *clientForm) View(m *Model) string {
	w := modalWidth(m, 90)
	f.name.Width, f.user.Width = w-24, w-24
	title := "New client"
	if f.id != "" {
		title = "Edit client"
	}
	var b strings.Builder
	row := func(i int, label, value string) {
		l := pad(label, 14)
		if f.focus == i {
			b.WriteString(sKey.Render("▸ ") + sBold.Render(l) + value + "\n")
		} else {
			b.WriteString("  " + l + value + "\n")
		}
	}
	row(0, "Name", f.name.View())
	row(1, "Username", f.user.View())
	b.WriteString("\n" + sHeader.Render("  Remotes this client can access (space to tick, p for a sub-folder):") + "\n")
	if len(f.items) == 0 {
		b.WriteString(sMuted.Render("  no remotes configured yet") + "\n")
	}
	// border 2, title 2, name/user 2, header 2, save 2, error 2, footer 3
	h := m.bodyHeight() - 15
	if h < 3 {
		h = 3
	}
	cur := f.focus - 2
	if cur < 0 {
		cur = 0
	}
	start, end := window(cur, len(f.items), h)
	for i := start; i < end; i++ {
		it := f.items[i]
		box := "[ ]"
		if it.checked {
			box = sGreen.Render("[x]")
		}
		label := it.remote
		if it.path != "" {
			label += ":" + it.path
		}
		detail := it.detail
		if it.missing {
			detail = sRed.Render(detail)
		} else {
			detail = sMuted.Render(trunc(detail, w-40))
		}
		line := box + " " + pad(label, 28) + " " + detail
		if f.focus == i+2 {
			b.WriteString(sKey.Render("▸ ") + line + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	save := "[ Save ]"
	if f.focus == f.count()-1 {
		save = sTabActive.Render("Save")
	}
	b.WriteString("\n  " + save)
	if f.err != "" {
		b.WriteString("\n\n" + sErr.Render(f.err))
	}
	footer := wrapHints(w-4, "tab/↑↓", "move", "space", "tick remote", "p", "sub-folder", "ctrl+s", "save", "esc", "cancel")
	if f.id == "" {
		footer = sMuted.Render("A password is generated when you save.") + "\n" + footer
	}
	return frame(title, b.String(), footer, w)
}
