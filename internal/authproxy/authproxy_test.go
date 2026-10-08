package authproxy

import (
	"testing"

	"github.com/nosini/rclone-proxy-tui/internal/state"
)

func testState() *state.State {
	st := state.Default()
	st.Clients = []*state.Client{
		{ID: "1", Name: "Laptop", Username: "laptop", Password: "secret", Shares: []state.Share{
			{Remote: "gdrive"}, {Remote: "my crypt", Path: "docs"},
		}},
		{ID: "2", Name: "Off", Username: "off", Password: "pw", Disabled: true, Shares: []state.Share{{Remote: "gdrive"}}},
		{ID: "3", Name: "Empty", Username: "empty", Password: "pw"},
	}
	return st
}

func TestResolve(t *testing.T) {
	st := testState()
	cfg, err := Resolve(st, state.WebDAV, Request{User: "laptop", Pass: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg["type"] != "combine" || cfg["_root"] != "" {
		t.Fatalf("bad cfg %v", cfg)
	}
	want := `gdrive=gdrive: "my crypt=my crypt:docs"`
	if cfg["upstreams"] != want {
		t.Fatalf("upstreams = %q, want %q", cfg["upstreams"], want)
	}
	if _, ok := cfg["_secret_access_key"]; ok {
		t.Fatal("secret leaked to non-S3 protocol")
	}
}

func TestResolveRefusals(t *testing.T) {
	st := testState()
	for _, tc := range []struct {
		name  string
		proto state.Protocol
		req   Request
	}{
		{"wrong password", state.WebDAV, Request{User: "laptop", Pass: "nope"}},
		{"empty password", state.SFTP, Request{User: "laptop"}},
		{"public key", state.SFTP, Request{User: "laptop", PublicKey: "AAAA"}},
		{"unknown", state.WebDAV, Request{User: "ghost", Pass: "secret"}},
		{"disabled", state.WebDAV, Request{User: "off", Pass: "pw"}},
		{"disabled s3", state.S3, Request{User: "off"}},
		{"no shares", state.FTP, Request{User: "empty", Pass: "pw"}},
	} {
		if _, err := Resolve(st, tc.proto, tc.req); err == nil {
			t.Errorf("%s: expected refusal", tc.name)
		}
	}
}

func TestResolveS3(t *testing.T) {
	cfg, err := Resolve(testState(), state.S3, Request{User: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg["_secret_access_key"] != "secret" {
		t.Fatalf("secret = %q", cfg["_secret_access_key"])
	}
}

func TestUpstreamsDuplicateFolder(t *testing.T) {
	_, err := Upstreams([]state.Share{{Remote: "a"}, {Remote: "b", Folder: "a"}})
	if err == nil {
		t.Fatal("expected duplicate folder error")
	}
}
