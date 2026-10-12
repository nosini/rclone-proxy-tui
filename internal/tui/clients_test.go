package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nosini/rclone-proxy-tui/internal/rclone"
	"github.com/nosini/rclone-proxy-tui/internal/state"
)

func TestClientFormRootNeedsOneRemote(t *testing.T) {
	m := &Model{st: state.Default(), paths: state.Paths{Dir: t.TempDir()},
		remotes: []rclone.Remote{{Name: "a", Type: "local"}, {Name: "b", Type: "local"}}}
	f := newClientForm(m, nil)
	for _, it := range f.items {
		it.checked = true
	}
	f.setFocus(f.rootFocus())
	f.Update(m, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	if !f.asRoot {
		t.Fatal("space did not tick the top level option")
	}
	if done, _ := f.save(m); done || f.err == "" {
		t.Fatal("saved a top level client with two remotes")
	}
	if len(m.st.Clients) != 0 {
		t.Fatal("client was stored")
	}
}
