// Package tui is the terminal user interface.
package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aymanbagabas/go-osc52/v2"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/nosini/rclone-proxy-tui/internal/rclone"
	"github.com/nosini/rclone-proxy-tui/internal/service"
	"github.com/nosini/rclone-proxy-tui/internal/state"
)

const (
	tabRemotes = iota
	tabClients
	tabServer
	numTabs
)

var tabNames = []string{"Remotes", "Clients", "Server"}

// Model is the root Bubble Tea model.
type Model struct {
	paths state.Paths
	st    *state.State
	rc    *rclone.Rclone
	svc   *service.Manager

	status   *state.Status
	daemonUp bool

	remotes       []rclone.Remote
	remotesErr    error
	remotesLoaded bool
	backends      []rclone.Backend

	tab      int
	cursor   [numTabs]int
	selected map[string]bool

	modals []Modal

	flash    string
	flashErr bool
	flashAt  time.Time
	busy     string

	w, h int
}

// Run starts the TUI.
func Run(paths state.Paths) error {
	m, err := newModel(paths)
	if err != nil {
		return err
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}

func newModel(paths state.Paths) (*Model, error) {
	// Fill in the rclone binary and config file on first run so the daemon
	// (which may run under systemd with a different PATH) uses the same ones.
	st, err := paths.Update(func(s *state.State) error {
		if s.RcloneBinary == "" {
			if bin, err := rclone.DetectBinary(); err == nil {
				s.RcloneBinary = bin
			}
		}
		if s.RcloneConfig == "" && s.RcloneBinary != "" {
			if cfg, err := rclone.DefaultConfigFile(s.RcloneBinary); err == nil {
				s.RcloneConfig = cfg
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	m := &Model{
		paths:    paths,
		st:       st,
		rc:       &rclone.Rclone{Bin: st.RcloneBinary, Config: st.RcloneConfig},
		svc:      service.New(paths),
		selected: map[string]bool{},
		w:        100,
		h:        30,
	}
	m.refreshStatus()
	return m, nil
}

// ---- messages ----

type tickMsg time.Time

type remotesMsg struct {
	remotes []rclone.Remote
	err     error
}

type flashMsg struct {
	text string
	err  bool
}

type busyMsg string

type doneMsg struct {
	text   string
	err    error
	reload bool
	then   func(m *Model) tea.Cmd
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) loadRemotes() tea.Cmd {
	rc := *m.rc
	return func() tea.Msg {
		r, err := rc.Remotes()
		return remotesMsg{r, err}
	}
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tick(), m.loadRemotes()}
	if m.rc.Bin == "" {
		cmds = append(cmds, m.flashCmd(rclone.ErrNotFound.Error(), true))
	}
	return tea.Batch(cmds...)
}

func (m *Model) flashCmd(text string, isErr bool) tea.Cmd {
	return func() tea.Msg { return flashMsg{text, isErr} }
}

func (m *Model) setFlash(text string, isErr bool) {
	m.flash, m.flashErr, m.flashAt = text, isErr, time.Now()
}

// async runs fn in the background, showing busy text meanwhile.
func (m *Model) async(busy string, fn func() doneMsg) tea.Cmd {
	m.busy = busy
	return func() tea.Msg { return fn() }
}

func (m *Model) refreshStatus() {
	m.status = m.paths.LoadStatus()
	m.daemonUp = m.status != nil
}

// save applies fn to the state and persists it.
func (m *Model) save(fn func(s *state.State) error) error {
	st, err := m.paths.Update(fn)
	if err != nil {
		m.setFlash(err.Error(), true)
		return err
	}
	m.st = st
	return nil
}

func (m *Model) push(md Modal) { m.modals = append(m.modals, md) }

func (m *Model) remove(md Modal) {
	for i := len(m.modals) - 1; i >= 0; i-- {
		if m.modals[i] == md {
			m.modals = append(m.modals[:i], m.modals[i+1:]...)
			return
		}
	}
}

func (m *Model) remoteByName(name string) *rclone.Remote {
	for i := range m.remotes {
		if m.remotes[i].Name == name {
			return &m.remotes[i]
		}
	}
	return nil
}

// ensureDaemon starts the daemon if it isn't running, so a freshly created
// client works straight away.
func (m *Model) ensureDaemon() tea.Cmd {
	if m.daemonUp || m.svc.Running() {
		return nil
	}
	svc := m.svc
	return func() tea.Msg {
		if err := svc.Start(); err != nil {
			return doneMsg{err: fmt.Errorf("could not start daemon: %w", err)}
		}
		return doneMsg{text: "Started the server daemon (" + svc.Mode() + ")"}
	}
}

func copyToClipboard(s string) {
	seq := osc52.New(s)
	if os.Getenv("TMUX") != "" {
		seq = seq.Tmux()
	} else if strings.HasPrefix(os.Getenv("TERM"), "screen") {
		seq = seq.Screen()
	}
	_, _ = seq.WriteTo(os.Stdout)
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.update(msg)
	// Follow edits of the rclone binary / config file paths.
	if m.rc.Bin != m.st.RcloneBinary || m.rc.Config != m.st.RcloneConfig {
		m.rc = &rclone.Rclone{Bin: m.st.RcloneBinary, Config: m.st.RcloneConfig}
		m.backends = nil
		m.remotesLoaded = false
		cmd = tea.Batch(cmd, m.loadRemotes())
	}
	return model, cmd
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		m.refreshStatus()
		if st, err := m.paths.Load(); err == nil {
			m.st = st
		}
		if m.flash != "" && time.Since(m.flashAt) > 8*time.Second {
			m.flash = ""
		}
		return m, tick()
	case remotesMsg:
		m.remotesLoaded = true
		m.remotes, m.remotesErr = msg.remotes, msg.err
		if m.cursor[tabRemotes] >= len(m.remotes) {
			m.cursor[tabRemotes] = max(0, len(m.remotes)-1)
		}
		for name := range m.selected {
			if m.remoteByName(name) == nil {
				delete(m.selected, name)
			}
		}
		return m, nil
	case flashMsg:
		m.setFlash(msg.text, msg.err)
		return m, nil
	case busyMsg:
		m.busy = string(msg)
		return m, nil
	case doneMsg:
		m.busy = ""
		var cmds []tea.Cmd
		if msg.err != nil {
			m.setFlash(msg.err.Error(), true)
		} else if msg.text != "" {
			m.setFlash(msg.text, false)
		}
		if msg.reload {
			cmds = append(cmds, m.loadRemotes())
		}
		if msg.then != nil && msg.err == nil {
			cmds = append(cmds, msg.then(m))
		}
		m.refreshStatus()
		return m, tea.Batch(cmds...)
	case remoteSavedMsg:
		return m, m.handleRemoteSaved(msg)
	case tea.KeyMsg:
		return m, m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleKey(k tea.KeyMsg) tea.Cmd {
	if k.String() == "ctrl+c" {
		return tea.Quit
	}
	if len(m.modals) > 0 {
		top := m.modals[len(m.modals)-1]
		done, cmd := top.Update(m, k)
		if done {
			m.remove(top)
		}
		return cmd
	}
	switch k.String() {
	case "q":
		return tea.Quit
	case "1", "2", "3":
		m.tab = int(k.String()[0] - '1')
		return nil
	case "tab", "right":
		m.tab = (m.tab + 1) % numTabs
		return nil
	case "shift+tab", "left":
		m.tab = (m.tab + numTabs - 1) % numTabs
		return nil
	case "?":
		m.push(newInfoModal("Help", helpText))
		return nil
	}
	switch m.tab {
	case tabRemotes:
		return m.remotesKey(k)
	case tabClients:
		return m.clientsKey(k)
	case tabServer:
		return m.serverKey(k)
	}
	return nil
}

func moveCursor(cur *int, k string, n, page int) bool {
	switch k {
	case "up", "k":
		*cur--
	case "down", "j":
		*cur++
	case "pgup":
		*cur -= page
	case "pgdown":
		*cur += page
	case "home", "g":
		*cur = 0
	case "end", "G":
		*cur = n - 1
	default:
		return false
	}
	if *cur >= n {
		*cur = n - 1
	}
	if *cur < 0 {
		*cur = 0
	}
	return true
}

func (m *Model) bodyHeight() int {
	h := m.h - 5
	if h < 3 {
		h = 3
	}
	return h
}

// View implements tea.Model.
func (m *Model) View() string {
	header := m.headerView()
	sep := sMuted.Render(strings.Repeat("─", max(0, m.w)))
	bodyH := m.bodyHeight()
	var body, footer string
	if len(m.modals) > 0 {
		body = lipgloss.Place(m.w, bodyH, lipgloss.Center, lipgloss.Center, m.modals[len(m.modals)-1].View(m))
		footer = ""
	} else {
		switch m.tab {
		case tabRemotes:
			body, footer = m.remotesView(bodyH)
		case tabClients:
			body, footer = m.clientsView(bodyH)
		case tabServer:
			body, footer = m.serverView(bodyH)
		}
	}
	// Footer: hints may take two lines; the line above is for messages.
	footerLines := strings.Split(footer, "\n")
	if len(footerLines) > 2 {
		footerLines = footerLines[:2]
	}
	for len(footerLines) < 2 {
		footerLines = append([]string{""}, footerLines...)
	}
	body = fitHeight(body, bodyH-len(footerLines)+2)
	msg := ""
	switch {
	case m.busy != "":
		msg = sYellow.Render("⏳ " + m.busy)
	case m.flash != "":
		text := strings.ReplaceAll(m.flash, "\n", " │ ")
		if m.flashErr {
			msg = sErr.Render("✗ " + text)
		} else {
			msg = sGreen.Render("✓ " + text)
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		header, sep, body, trunc(msg, m.w), footerLines[0], footerLines[1])
}

func fitHeight(s string, h int) string {
	if h < 1 {
		h = 1
	}
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m *Model) headerView() string {
	left := sTitle.Render("rclone-proxy-tui") + " "
	for i, name := range tabNames {
		label := fmt.Sprintf("%d %s", i+1, name)
		if i == m.tab {
			left += sTabActive.Render(label)
		} else {
			left += sTab.Render(label)
		}
	}
	var right string
	if !m.daemonUp {
		right = sRed.Render("○ daemon stopped") + sMuted.Render(" (Server tab → start)")
	} else {
		var parts []string
		for _, p := range state.AllProtocols {
			pc := m.st.Protocols[p]
			if pc == nil || !pc.Enabled {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s %s:%d", dot(m.status.Protocols[p], true), p, pc.Port))
		}
		if len(parts) == 0 {
			parts = []string{sYellow.Render("no protocols enabled")}
		}
		right = sGreen.Render("● daemon") + "  " + strings.Join(parts, "  ")
	}
	gap := m.w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		// Narrow terminal: just the daemon state.
		right = sGreen.Render("● daemon")
		if !m.daemonUp {
			right = sRed.Render("○ daemon stopped")
		}
		gap = m.w - lipgloss.Width(left) - lipgloss.Width(right)
	}
	if gap < 1 {
		return trunc(left, m.w)
	}
	return left + strings.Repeat(" ", gap) + right
}

const helpText = `rclone-proxy-tui manages rclone remotes and re-exposes them to clients.

How it works
  • Remotes are the normal rclone remotes in your rclone.conf (crypt, alias,
    union etc. all work, because clients are served the named remote).
  • A client is a login (username + generated password) that can see one or
    more remotes, each as a top level folder. The same remote can be shared
    with as many clients as you like.
  • A background daemon runs one rclone server per enabled protocol
    (WebDAV, SFTP, S3, FTP, HTTP). Every client logs in to the same ports with
    its own credentials. Changes apply within a second.

Quick start
  1  Remotes tab: press n to add a remote (or c for rclone's own config).
  2  Press space on the remotes to share, then s, then enter.
  3  The new client's login and URLs pop up. Done.

Everywhere
  1 2 3 / tab        switch tab           ?   this help
  ↑↓ j k pgup pgdn   move                 q   quit (the daemon keeps running)

Remotes tab
  space  select remote          s / enter  share selection as a new client
  a      add selection to an existing client
  n new remote · e edit · d delete · t test · o re-run OAuth login
  c open rclone config · r reload

Clients tab
  enter  show login + connection info (y copy password, w save to file)
  n new · e edit (pick remotes) · p new password · x enable/disable · d delete

Server tab
  enter  change setting / start-stop daemon   space  toggle protocol
  f      extra rclone flags for a protocol    R restart daemon · l log
`
