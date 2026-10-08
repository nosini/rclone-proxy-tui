package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nosini/rclone-proxy-tui/internal/service"
	"github.com/nosini/rclone-proxy-tui/internal/state"
)

func TestMain(m *testing.M) {
	if os.Getenv("RCLONE_PROXY_TUI_TEST_CHILD") == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// Exercise the real CLI parser through the same start path used by the TUI.
func TestManualDaemonCustomDir(t *testing.T) {
	t.Setenv("RCLONE_PROXY_TUI_TEST_CHILD", "1")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(state.EnvDir, "")
	bin, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	paths := state.Paths{Dir: filepath.Join(t.TempDir(), "custom state")}
	st := state.Default()
	st.RcloneBinary = bin
	for _, pc := range st.Protocols {
		pc.Enabled = false
	}
	if err := paths.Save(st); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	svc := &service.Manager{Paths: paths, Exe: exe}
	defer func() {
		if err := svc.Stop(); err != nil {
			t.Error(err)
		}
		// Also reap a daemon started in the wrong directory by a regression.
		fallback := service.New(state.Paths{Dir: state.DefaultDir()})
		if err := fallback.Stop(); err != nil {
			t.Error(err)
		}
	}()
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for paths.LoadStatus() == nil && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if paths.LoadStatus() == nil {
		t.Fatal("daemon never published status in the custom directory")
	}
	if _, err := os.Stat(filepath.Join(state.DefaultDir(), "daemon.pid")); !os.IsNotExist(err) {
		t.Fatal("daemon used the default directory")
	}
	if err := svc.Restart(); err != nil {
		t.Fatal(err)
	}
	if !svc.Running() {
		t.Fatal("restarted daemon is not running")
	}
}
