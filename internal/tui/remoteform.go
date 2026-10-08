package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/nosini/rclone-proxy-tui/internal/rclone"
	"github.com/nosini/rclone-proxy-tui/internal/state"
)

// rfField is one backend option in the form.
type rfField struct {
	opt      rclone.Option
	in       textinput.Model
	orig     string // value currently in rclone.conf (edit mode)
	reveal   bool
	examples []rclone.Example
}

func (f *rfField) value() string {
	if f.opt.IsPassword {
		return f.in.Value()
	}
	return strings.TrimSpace(f.in.Value())
}

// remoteForm creates or edits a remote using the option list rclone itself
// reports, so every backend rclone supports can be configured.
type remoteForm struct {
	backend rclone.Backend
	edit    *rclone.Remote
	name    textinput.Model
	fields  []*rfField
	focus   string // "" = name, option name, or "\x00save"
	adv     bool
	err     string
	saving  bool
}

const saveKey = "\x00save"

var remoteNameRe = regexp.MustCompile(`^[\p{L}\p{N}_.+@][\p{L}\p{N}_.+@ -]*$`)

func wrapsRemote(backend, opt string) bool {
	switch backend {
	case "crypt", "alias", "chunker", "compress", "hasher", "cache":
		return opt == "remote"
	}
	return false
}

func newRemoteForm(m *Model, b rclone.Backend, edit *rclone.Remote) *remoteForm {
	f := &remoteForm{backend: b, edit: edit}
	name := ""
	if edit != nil {
		name = edit.Name
	}
	f.name = newInput(name, "e.g. "+b.Name, 40)
	for _, o := range b.Options {
		// Bit 2 hides an option from the configurator; bit 1 only hides CLI flags.
		if o.Hide&2 != 0 {
			continue
		}
		fld := &rfField{opt: o, examples: o.Examples}
		if o.Type == "bool" && len(fld.examples) == 0 {
			fld.examples = []rclone.Example{{Value: "true"}, {Value: "false"}}
		}
		if wrapsRemote(b.Name, o.Name) && len(fld.examples) == 0 {
			for _, r := range m.remotes {
				if edit == nil || r.Name != edit.Name {
					fld.examples = append(fld.examples, rclone.Example{Value: r.Name + ":", Help: r.Type + " " + r.Detail()})
				}
			}
		}
		val := ""
		placeholder := o.DefaultStr
		if edit != nil {
			cur, ok := edit.Config[o.Name]
			if o.IsPassword {
				if ok && cur != "" {
					placeholder = "unchanged - type to replace"
				}
			} else {
				val = cur
				fld.orig = cur
			}
			if ok && cur != "" && o.Advanced {
				f.adv = true
			}
		}
		if placeholder != "" && !o.IsPassword {
			placeholder = "default: " + placeholder
		}
		fld.in = newInput(val, placeholder, 40)
		if o.IsPassword {
			fld.in.EchoMode = textinput.EchoPassword
			fld.in.EchoCharacter = '•'
		}
		f.fields = append(f.fields, fld)
	}
	if edit != nil {
		vis := f.visible()
		if len(vis) > 0 {
			f.setFocus(vis[0].opt.Name)
		} else {
			f.setFocus(saveKey)
		}
	} else {
		f.setFocus("")
	}
	return f
}

func providerMatch(filter, provider string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true
	}
	neg := strings.HasPrefix(filter, "!")
	filter = strings.TrimPrefix(filter, "!")
	match := false
	for _, p := range strings.Split(filter, ",") {
		if strings.TrimSpace(p) == provider {
			match = true
			break
		}
	}
	return match != neg
}

func (f *remoteForm) provider() string {
	for _, fld := range f.fields {
		if fld.opt.Name == "provider" {
			return fld.value()
		}
	}
	return ""
}

// visible returns the options to show: basic ones (plus advanced when
// toggled) that apply to the chosen provider.
func (f *remoteForm) visible() []*rfField {
	prov := f.provider()
	var out []*rfField
	for _, fld := range f.fields {
		if fld.opt.Advanced && !f.adv {
			continue
		}
		if fld.opt.Provider != "" && (prov == "" || !providerMatch(fld.opt.Provider, prov)) {
			continue
		}
		out = append(out, fld)
	}
	return out
}

func (f *remoteForm) examplesFor(fld *rfField) []rclone.Example {
	prov := f.provider()
	var out []rclone.Example
	for _, e := range fld.examples {
		if e.Provider == "" || providerMatch(e.Provider, prov) {
			out = append(out, e)
		}
	}
	return out
}

// order is the focus order: name (new remotes only), fields, save.
func (f *remoteForm) order() []string {
	var keys []string
	if f.edit == nil {
		keys = append(keys, "")
	}
	for _, fld := range f.visible() {
		keys = append(keys, fld.opt.Name)
	}
	return append(keys, saveKey)
}

func (f *remoteForm) field(name string) *rfField {
	if name == "" || name == saveKey {
		return nil
	}
	for _, fld := range f.fields {
		if fld.opt.Name == name {
			return fld
		}
	}
	return nil
}

func (f *remoteForm) setFocus(key string) {
	f.focus = key
	f.name.Blur()
	for _, fld := range f.fields {
		fld.in.Blur()
	}
	if key == "" {
		f.name.Focus()
	} else if fld := f.field(key); fld != nil {
		fld.in.Focus()
	}
}

func (f *remoteForm) moveFocus(d int) {
	keys := f.order()
	idx := 0
	for i, k := range keys {
		if k == f.focus {
			idx = i
		}
	}
	idx = (idx + d + len(keys)) % len(keys)
	f.setFocus(keys[idx])
}

// fixFocus keeps focus on something visible after the field set changes.
func (f *remoteForm) fixFocus() {
	for _, k := range f.order() {
		if k == f.focus {
			return
		}
	}
	f.setFocus(saveKey)
}

func (f *remoteForm) cycle(fld *rfField, d int) bool {
	ex := f.examplesFor(fld)
	if len(ex) == 0 {
		return false
	}
	cur := fld.value()
	idx := -1
	for i, e := range ex {
		if e.Value == cur {
			idx = i
		}
	}
	if idx < 0 && cur != "" {
		return false // free text typed: arrows move the cursor instead
	}
	switch {
	case idx < 0 && d > 0:
		idx = 0
	case idx < 0:
		idx = len(ex) - 1
	default:
		idx = (idx + d + len(ex)) % len(ex)
	}
	fld.in.SetValue(ex[idx].Value)
	fld.in.CursorEnd()
	return true
}

func (f *remoteForm) Update(m *Model, k tea.KeyMsg) (bool, tea.Cmd) {
	if f.saving {
		if k.String() == "esc" {
			return true, nil
		}
		return false, nil
	}
	fld := f.field(f.focus)
	switch k.String() {
	case "esc":
		return true, nil
	case "tab", "down":
		f.moveFocus(1)
		return false, nil
	case "shift+tab", "up":
		f.moveFocus(-1)
		return false, nil
	case "enter":
		if f.focus == saveKey {
			return false, f.save(m)
		}
		f.moveFocus(1)
		return false, nil
	case "ctrl+s":
		return false, f.save(m)
	case "ctrl+a":
		f.adv = !f.adv
		f.fixFocus()
		return false, nil
	case "ctrl+g":
		if fld != nil && fld.opt.IsPassword {
			fld.in.SetValue(state.GeneratePassword(32))
			fld.reveal = true
			fld.in.EchoMode = textinput.EchoNormal
			return false, nil
		}
	case "ctrl+r":
		if fld != nil && fld.opt.IsPassword {
			fld.reveal = !fld.reveal
			if fld.reveal {
				fld.in.EchoMode = textinput.EchoNormal
			} else {
				fld.in.EchoMode = textinput.EchoPassword
			}
			return false, nil
		}
	case "left", "right":
		if fld != nil {
			d := 1
			if k.String() == "left" {
				d = -1
			}
			if f.cycle(fld, d) {
				f.fixFocus()
				return false, nil
			}
		}
	}
	var cmd tea.Cmd
	if f.focus == "" {
		f.name, cmd = f.name.Update(k)
	} else if fld != nil {
		fld.in, cmd = fld.in.Update(k)
		if fld.opt.Name == "provider" {
			f.fixFocus()
		}
	}
	f.err = ""
	return false, cmd
}

type remoteSavedMsg struct {
	form *remoteForm
	name string
	res  *rclone.ConfigResult
	err  error
}

func (f *remoteForm) save(m *Model) tea.Cmd {
	name := strings.TrimSpace(f.name.Value())
	kv := map[string]string{}
	if f.edit == nil {
		if name == "" {
			f.err = "enter a name for the remote"
			f.setFocus("")
			return nil
		}
		if !remoteNameRe.MatchString(name) || strings.HasSuffix(name, " ") {
			f.err = "names may contain letters, digits, space, _ - . + @ (not at the start)"
			f.setFocus("")
			return nil
		}
		if m.remoteByName(name) != nil {
			f.err = "a remote called " + name + " already exists"
			f.setFocus("")
			return nil
		}
	} else {
		name = f.edit.Name
	}
	visible := map[*rfField]bool{}
	for _, fld := range f.visible() {
		visible[fld] = true
	}
	for _, fld := range f.fields {
		v := fld.value()
		o := fld.opt
		if f.edit != nil {
			if o.IsPassword {
				if v != "" {
					kv[o.Name] = v
				}
				continue
			}
			if v != fld.orig {
				kv[o.Name] = v
			}
			continue
		}
		if v == "" {
			if o.Required && o.DefaultStr == "" && visible[fld] {
				f.err = o.Name + " is required"
				f.setFocus(o.Name)
				return nil
			}
			continue
		}
		if !visible[fld] && !o.Advanced {
			continue // belongs to another provider
		}
		if v == o.DefaultStr && !o.Required {
			continue
		}
		kv[o.Name] = v
	}
	if f.edit != nil && len(kv) == 0 {
		f.err = "nothing changed"
		return nil
	}
	f.saving = true
	f.err = ""
	rc, typ, edit := *m.rc, f.backend.Name, f.edit != nil
	return func() tea.Msg {
		var res *rclone.ConfigResult
		var err error
		if edit {
			res, err = rc.Update(name, kv)
		} else {
			res, err = rc.Create(name, typ, kv)
		}
		return remoteSavedMsg{form: f, name: name, res: res, err: err}
	}
}

func (m *Model) handleRemoteSaved(msg remoteSavedMsg) tea.Cmd {
	f := msg.form
	f.saving = false
	if msg.err != nil {
		f.err = msg.err.Error()
		return nil
	}
	m.remove(f)
	verb := "Created"
	if f.edit != nil {
		verb = "Updated"
	}
	m.setFlash(verb+" remote "+msg.name, false)
	if msg.res.NeedsMore() {
		name := msg.name
		q := ""
		if msg.res.Option != nil {
			q = "\n\nrclone's next question: " + firstLine(msg.res.Option.Help)
		}
		m.push(&confirmModal{title: "Finish setting up " + name, yes: "continue in rclone",
			text: name + " needs a few more interactive steps, usually a login (OAuth)." + q +
				"\n\nThe terminal is handed to rclone and you come back here when it's done. " +
				"Over SSH: answer 'n' to 'Use web browser to automatically authenticate?' and rclone prints a command to run on a computer with a browser.",
			onYes: func(m *Model) tea.Cmd { return m.reconnectCmd(name) }})
	}
	return m.loadRemotes()
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}

func (f *remoteForm) View(m *Model) string {
	w := modalWidth(m, 110)
	labelW := 28
	inW := w - labelW - 22
	if inW < 20 {
		inW = 20
	}
	title := "New " + f.backend.Name + " remote"
	if f.edit != nil {
		title = "Edit " + f.edit.Name + " (" + f.backend.Name + ")"
	}
	var b strings.Builder
	b.WriteString(sMuted.Render(trunc(f.backend.Description, w-4)) + "\n\n")

	type line struct {
		key  string
		text string
	}
	var lines []line
	nameView := ""
	if f.edit == nil {
		f.name.Width = inW
		nameView = f.name.View()
		lines = append(lines, line{"", f.row(f.focus == "", "* name", nameView, "", labelW)})
	}
	for _, fld := range f.visible() {
		fld.in.Width = inW
		label := fld.opt.Name
		if fld.opt.Required {
			label = "* " + label
		} else {
			label = "  " + label
		}
		extra := ""
		if ex := f.examplesFor(fld); len(ex) > 0 {
			extra = sMuted.Render(fmt.Sprintf(" ◂▸ %d", len(ex)))
		}
		if fld.opt.Advanced {
			extra += sMuted.Render(" adv")
		}
		lines = append(lines, line{fld.opt.Name, f.row(f.focus == fld.opt.Name, label, fld.in.View(), extra, labelW)})
	}
	save := "  [ Save ]"
	if f.saving {
		save = sYellow.Render("  saving…")
	} else if f.focus == saveKey {
		save = "  " + sTabActive.Render("Save")
	}
	lines = append(lines, line{saveKey, "\n" + save})

	var hb strings.Builder
	// Help for the focused option.
	if fld := f.field(f.focus); fld != nil {
		help := strings.TrimSpace(fld.opt.Help)
		maxHelp, maxEx := 5, 6
		if m.h < 40 {
			maxHelp, maxEx = 2, 4
		}
		hl := strings.Split(wordWrap(help, w-6), "\n")
		if len(hl) > maxHelp {
			hl = append(hl[:maxHelp], "…")
		}
		hb.WriteString("\n" + sBold.Render(fld.opt.Name) + "\n" + sMuted.Render(strings.Join(hl, "\n")))
		if ex := f.examplesFor(fld); len(ex) > 0 {
			hb.WriteString("\n")
			cur := fld.value()
			for i, e := range ex {
				if i >= maxEx {
					hb.WriteString(sMuted.Render(fmt.Sprintf("\n  … %d more (◂▸ to cycle)", len(ex)-maxEx)))
					break
				}
				mark := "  "
				if e.Value == cur {
					mark = sGreen.Render("▸ ")
				}
				hb.WriteString("\n" + mark + trunc(pad(e.Value, 24)+" "+sMuted.Render(firstLine(e.Help)), w-8))
			}
		}
		if wrapsRemote(f.backend.Name, fld.opt.Name) {
			hb.WriteString("\n\n" + sMuted.Render("Tip: use remote:path, e.g. gdrive:encrypted - the wrapped remote must exist."))
		}
		if fld.opt.IsPassword && f.backend.Name == "crypt" {
			hb.WriteString("\n\n" + sYellow.Render("Back up crypt passwords somewhere safe: without them the files can't be decrypted."))
		}
	} else if f.focus == "" {
		hb.WriteString("\n" + sMuted.Render("The name you'll see in the remote list and that clients see as a folder."))
	}
	if f.backend.NeedsOAuth() && f.edit == nil {
		hb.WriteString("\n\n" + sYellow.Render("This type needs a login: after saving, rclone walks you through it (works over SSH)."))
	}
	if f.err != "" {
		hb.WriteString("\n\n" + sErr.Render(f.err))
	}
	// Everything except the field list: border 2, title 2, description 2,
	// more markers 2, help, footer 3.
	help := hb.String()
	h := m.bodyHeight() - 11 - strings.Count(help, "\n") - 1
	if h < 3 {
		h = 3
	}
	cur := 0
	for i, l := range lines {
		if l.key == f.focus {
			cur = i
		}
	}
	start, end := window(cur, len(lines), h)
	if start > 0 {
		b.WriteString(sMuted.Render("  ↑ more") + "\n")
	}
	for _, l := range lines[start:end] {
		b.WriteString(l.text + "\n")
	}
	if end < len(lines) {
		b.WriteString(sMuted.Render("  ↓ more") + "\n")
	}

	b.WriteString(help)
	pairs := []string{"tab/↑↓", "move", "◂▸", "choose", "ctrl+a", map[bool]string{true: "hide advanced", false: "show advanced"}[f.adv]}
	if fld := f.field(f.focus); fld != nil && fld.opt.IsPassword {
		pairs = append(pairs, "ctrl+g", "generate", "ctrl+r", "reveal")
	}
	pairs = append(pairs, "ctrl+s", "save", "esc", "cancel")
	return frame(title, strings.TrimRight(b.String(), "\n"), wrapHints(w-4, pairs...), w)
}

func (f *remoteForm) row(focused bool, label, input, extra string, labelW int) string {
	if focused {
		return sKey.Render("▸ ") + sBold.Render(pad(label, labelW)) + "[" + input + "]" + extra
	}
	return "  " + pad(label, labelW) + " " + input + " " + extra
}
