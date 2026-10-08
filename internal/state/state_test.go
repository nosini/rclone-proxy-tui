package state

import (
	"os/exec"
	"strings"
	"testing"
)

func TestObscureMatchesRclone(t *testing.T) {
	bin, err := exec.LookPath("rclone")
	if err != nil {
		t.Skip("rclone not installed")
	}
	for _, pw := range []string{"hunter2", "äöü with spaces", GeneratePassword(20)} {
		out, err := exec.Command(bin, "reveal", Obscure(pw)).Output()
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(string(out)); got != pw {
			t.Fatalf("rclone reveal = %q, want %q", got, pw)
		}
	}
}

func TestUniqueUsername(t *testing.T) {
	s := Default()
	s.Clients = []*Client{{ID: "a", Username: "laptop"}, {ID: "b", Username: "laptop2"}}
	if got := s.UniqueUsername("Laptop", ""); got != "laptop3" {
		t.Fatalf("got %q", got)
	}
	if got := s.UniqueUsername("Laptop", "a"); got != "laptop" {
		t.Fatalf("rename kept: got %q", got)
	}
	if got := Slug("  My Phone (work)! "); got != "my-phone-work" {
		t.Fatalf("slug %q", got)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	p := Paths{Dir: t.TempDir()}
	st, err := p.Update(func(s *State) error {
		s.Clients = append(s.Clients, &Client{ID: "x", Username: "u", Password: "p", Shares: []Share{{Remote: "r"}}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.ClientByUsername("u") == nil || len(got.Protocols) != len(AllProtocols) || st.BindAddress != "0.0.0.0" {
		t.Fatalf("bad round trip: %+v", got)
	}
}

func TestShareFolderName(t *testing.T) {
	s := Share{Remote: "a/b=c"}
	if s.FolderName() != "a_b_c" || (Share{Remote: "g", Path: "/x"}).Target() != "g:x" {
		t.Fatal(s.FolderName())
	}
}
