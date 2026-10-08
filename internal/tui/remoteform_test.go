package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nosini/rclone-proxy-tui/internal/rclone"
)

func TestPasswordWhitespace(t *testing.T) {
	f := rfField{opt: rclone.Option{IsPassword: true}, in: newInput(" secret ", "", 40)}
	if f.value() != " secret " {
		t.Fatal("password whitespace was removed")
	}
}

func TestOptionVisibility(t *testing.T) {
	b := rclone.Backend{Options: []rclone.Option{
		{Name: "config_only", Hide: 1},
		{Name: "cli_only", Hide: 2},
		{Name: "hidden", Hide: 3},
	}}
	f := newRemoteForm(&Model{}, b, nil)
	if f.field("config_only") == nil || f.field("cli_only") != nil || f.field("hidden") != nil {
		t.Fatal("form did not follow configurator visibility bits")
	}
}

func TestPickerUnicodeBackspace(t *testing.T) {
	p := &pickModal{filter: "my café"}
	p.Update(&Model{}, tea.KeyMsg{Type: tea.KeyBackspace})
	if p.filter != "my caf" {
		t.Fatalf("filter = %q", p.filter)
	}
}
