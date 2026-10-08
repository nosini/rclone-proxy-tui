package daemon

import (
	"reflect"
	"testing"
)

func TestSplitArgs(t *testing.T) {
	got, err := SplitArgs(`--read-only --exclude "*.tmp" -v 'a b' c\ d`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--read-only", "--exclude", "*.tmp", "-v", "a b", "c d"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
	if _, err := SplitArgs(`"open`); err == nil {
		t.Fatal("expected error")
	}
}
