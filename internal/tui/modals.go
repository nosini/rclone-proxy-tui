package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Modal is a dialog drawn over the main view. Update returns done=true to
// close it.
type Modal interface {
	Update(m *Model, msg tea.KeyMsg) (done bool, cmd tea.Cmd)
	View(m *Model) string
}

func newInput(value, placeholder string, width int) textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = placeholder
	ti.SetValue(value)
	ti.CharLimit = 4096
	ti.Width = width
	ti.Cursor.SetMode(cursor.CursorStatic)
	ti.CursorEnd()
	return ti
}

func modalWidth(m *Model, want int) int {
	w := m.w - 6
	if w > want {
		w = want
	}
	if w < 30 {
		w = 30
	}
	return w
}

func frame(title, body, footer string, width int) string {
	var b strings.Builder
	b.WriteString(sTitle.Render(title))
	b.WriteString("\n\n")
	b.WriteString(body)
	if footer != "" {
		b.WriteString("\n\n")
		b.WriteString(footer)
	}
	return sModal.Width(width).Render(b.String())
}

// ---- confirm ----

type confirmModal struct {
	title string
	text  string
	yes   string
	onYes func(m *Model) tea.Cmd
}

func (c *confirmModal) Update(m *Model, k tea.KeyMsg) (bool, tea.Cmd) {
	switch k.String() {
	case "y", "Y", "enter":
		return true, c.onYes(m)
	case "n", "N", "esc", "q":
		return true, nil
	}
	return false, nil
}

func (c *confirmModal) View(m *Model) string {
	w := modalWidth(m, 70)
	yes := c.yes
	if yes == "" {
		yes = "yes"
	}
	return frame(c.title, wordWrap(c.text, w-4), hints("y/enter", yes, "n/esc", "cancel"), w)
}

// ---- input ----

type inputModal struct {
	title    string
	prompt   string
	input    textinput.Model
	err      string
	onSubmit func(m *Model, value string) (tea.Cmd, error)
}

func newInputModal(title, prompt, value string, onSubmit func(m *Model, value string) (tea.Cmd, error)) *inputModal {
	in := newInput(value, "", 50)
	in.Focus()
	return &inputModal{title: title, prompt: prompt, input: in, onSubmit: onSubmit}
}

func (im *inputModal) Update(m *Model, k tea.KeyMsg) (bool, tea.Cmd) {
	switch k.String() {
	case "esc":
		return true, nil
	case "enter":
		cmd, err := im.onSubmit(m, strings.TrimSpace(im.input.Value()))
		if err != nil {
			im.err = err.Error()
			return false, nil
		}
		return true, cmd
	}
	var cmd tea.Cmd
	im.input, cmd = im.input.Update(k)
	im.err = ""
	return false, cmd
}

func (im *inputModal) View(m *Model) string {
	w := modalWidth(m, 70)
	im.input.Width = w - 6
	body := ""
	if im.prompt != "" {
		body = wordWrap(im.prompt, w-4) + "\n\n"
	}
	body += lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(colAccent).Width(w - 4).Render(im.input.View())
	if im.err != "" {
		body += "\n" + sErr.Render(im.err)
	}
	return frame(im.title, body, hints("enter", "ok", "esc", "cancel"), w)
}

// ---- info (scrollable text) ----

type infoKey struct {
	key, label string
	fn         func(m *Model) tea.Cmd
}

type infoModal struct {
	title string
	text  string
	keys  []infoKey
	vp    viewport.Model
	ready bool
}

func newInfoModal(title, text string, keys ...infoKey) *infoModal {
	return &infoModal{title: title, text: strings.TrimRight(text, "\n"), keys: keys}
}

func (im *infoModal) layout(m *Model) int {
	w := modalWidth(m, 100)
	// border 2 + title 2 + footer gap 1 + footer up to 2 lines
	h := m.bodyHeight() - 8
	if h < 3 {
		h = 3
	}
	content := wordWrap(im.text, w-4)
	if lines := strings.Count(content, "\n") + 1; lines < h {
		h = lines
	}
	if !im.ready {
		im.vp = viewport.New(w-4, h)
		im.ready = true
	}
	im.vp.Width, im.vp.Height = w-4, h
	im.vp.SetContent(content)
	return w
}

func (im *infoModal) Update(m *Model, k tea.KeyMsg) (bool, tea.Cmd) {
	switch k.String() {
	case "esc", "q", "enter":
		return true, nil
	}
	for _, ik := range im.keys {
		if k.String() == ik.key {
			return false, ik.fn(m)
		}
	}
	im.layout(m)
	var cmd tea.Cmd
	im.vp, cmd = im.vp.Update(k)
	return false, cmd
}

func (im *infoModal) View(m *Model) string {
	w := im.layout(m)
	var pairs []string
	for _, ik := range im.keys {
		pairs = append(pairs, ik.key, ik.label)
	}
	if im.vp.TotalLineCount() > im.vp.Height {
		pairs = append(pairs, "↑↓/pgup/pgdn", "scroll")
	}
	pairs = append(pairs, "esc", "close")
	return frame(im.title, im.vp.View(), wrapHints(w-4, pairs...), w)
}

// ---- pick from a list, with type-to-filter ----

type pickItem struct {
	label  string
	detail string
	value  string
}

type pickModal struct {
	title  string
	prompt string
	items  []pickItem
	filter string
	cursor int
	onPick func(m *Model, item pickItem) tea.Cmd
}

func (p *pickModal) filtered() []pickItem {
	if p.filter == "" {
		return p.items
	}
	f := strings.ToLower(p.filter)
	var prefix, contains []pickItem
	for _, it := range p.items {
		l := strings.ToLower(it.label)
		switch {
		case strings.HasPrefix(l, f):
			prefix = append(prefix, it)
		case strings.Contains(l, f) || strings.Contains(strings.ToLower(it.detail), f):
			contains = append(contains, it)
		}
	}
	return append(prefix, contains...)
}

func (p *pickModal) Update(m *Model, k tea.KeyMsg) (bool, tea.Cmd) {
	items := p.filtered()
	switch k.Type {
	case tea.KeyEsc:
		if p.filter != "" {
			p.filter, p.cursor = "", 0
			return false, nil
		}
		return true, nil
	case tea.KeyEnter:
		if len(items) == 0 {
			return false, nil
		}
		return true, p.onPick(m, items[p.cursor])
	case tea.KeyUp:
		p.cursor--
	case tea.KeyDown, tea.KeyTab:
		p.cursor++
	case tea.KeyPgUp:
		p.cursor -= 10
	case tea.KeyPgDown:
		p.cursor += 10
	case tea.KeyHome:
		p.cursor = 0
	case tea.KeyEnd:
		p.cursor = len(items) - 1
	case tea.KeyBackspace:
		if p.filter != "" {
			_, size := utf8.DecodeLastRuneInString(p.filter)
			p.filter = p.filter[:len(p.filter)-size]
			p.cursor = 0
		}
	case tea.KeyRunes, tea.KeySpace:
		p.filter += string(k.Runes)
		if k.Type == tea.KeySpace {
			p.filter += " "
		}
		p.cursor = 0
	}
	items = p.filtered()
	if p.cursor >= len(items) {
		p.cursor = len(items) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
	return false, nil
}

func (p *pickModal) View(m *Model) string {
	w := modalWidth(m, 90)
	items := p.filtered()
	var b strings.Builder
	promptLines := 0
	if p.prompt != "" {
		pr := wordWrap(p.prompt, w-4)
		promptLines = strings.Count(pr, "\n") + 2
		b.WriteString(pr + "\n\n")
	}
	// border 2 + title 2 + filter 2 + footer 2
	h := m.bodyHeight() - 8 - promptLines
	if h < 3 {
		h = 3
	}
	b.WriteString(sMuted.Render("filter: ") + p.filter + sMuted.Render("▏") + "\n\n")
	labelW := 0
	for _, it := range items {
		if l := lipgloss.Width(it.label); l > labelW {
			labelW = l
		}
	}
	if labelW > 24 {
		labelW = 24
	}
	start, end := window(p.cursor, len(items), h)
	for i := start; i < end; i++ {
		it := items[i]
		line := " " + pad(it.label, labelW) + "  " + sMuted.Render(it.detail)
		line = pad(line, w-4)
		if i == p.cursor {
			line = sCursor.Render(pad(" "+pad(it.label, labelW)+"  "+it.detail, w-4))
		}
		b.WriteString(line + "\n")
	}
	if len(items) == 0 {
		b.WriteString(sMuted.Render(" no matches") + "\n")
	}
	return frame(p.title, strings.TrimRight(b.String(), "\n"), hints("type", "filter", "↑↓", "move", "enter", "choose", "esc", "cancel"), w)
}
