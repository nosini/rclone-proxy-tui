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
	for _, tc := range []struct {
		name, password, obscured string
	}{
		{name: "ASCII", password: "hunter2"},
		{name: "Unicode", password: "äöü with spaces"},
		{name: "random password", password: GeneratePassword(20)},
		{name: "whitespace", password: " leading and trailing "},
		// A valid encoding of hunter2 that starts with a CLI flag prefix.
		{name: "flag-like encoding", password: "hunter2", obscured: "-FmMNX42nSqxlf3rutmKjZpqnyyaYFs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded := tc.obscured
			if encoded == "" {
				encoded = Obscure(tc.password)
			}
			out, err := exec.Command(bin, "reveal", "--", encoded).CombinedOutput()
			if err != nil {
				t.Fatalf("rclone reveal: %v\n%s", err, out)
			}
			if got := strings.TrimSuffix(string(out), "\n"); got != tc.password {
				t.Fatalf("rclone reveal = %q, want %q", got, tc.password)
			}
		})
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
