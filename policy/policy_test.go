package policy

import "testing"

func TestNewStaticDefaultsToSymlinkDeny(t *testing.T) {
	pol := NewStatic(nil)

	if got, want := pol.SymlinkMode(), SymlinkDeny; got != want {
		t.Fatalf("SymlinkMode() = %q, want %q", got, want)
	}
}
