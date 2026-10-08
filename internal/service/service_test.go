package service

import (
	"strings"
	"testing"

	"github.com/nosini/rclone-proxy-tui/internal/state"
)

func TestUnit(t *testing.T) {
	m := &Manager{Paths: state.Paths{Dir: "/home/u/my dir"}, Exe: "/usr/local/bin/rclone-proxy-tui"}
	u := m.Unit()
	for _, want := range []string{
		`ExecStart=:"/usr/local/bin/rclone-proxy-tui" --dir "/home/u/my dir" daemon`,
		"Restart=on-failure",
		"TimeoutStopSec=90",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("unit missing %q:\n%s", want, u)
		}
	}
}

func TestUnitLiteralPaths(t *testing.T) {
	m := &Manager{Paths: state.Paths{Dir: "/home/u/100%/$state"}, Exe: "/opt/$bin%/proxy"}
	u := m.Unit()
	want := `ExecStart=:"/opt/$bin%%/proxy" --dir "/home/u/100%%/$state" daemon`
	if !strings.Contains(u, want) {
		t.Fatalf("unit expands literal path characters:\n%s", u)
	}
}
