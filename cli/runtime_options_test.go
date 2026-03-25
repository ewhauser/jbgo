package cli

import "testing"

func TestDarwinVarFoldersTempRoot(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		path string
		want string
		ok   bool
	}{
		{
			name: "var folders temp subtree",
			path: "/var/folders/ab/cdef123/T/gbash-cli",
			want: "/var/folders/ab/cdef123/T",
			ok:   true,
		},
		{
			name: "private var folders temp subtree",
			path: "/private/var/folders/ab/cdef123/T/gbash-cli",
			want: "/private/var/folders/ab/cdef123/T",
			ok:   true,
		},
		{
			name: "cache subtree is rejected",
			path: "/var/folders/ab/cdef123/C/com.apple.Safari",
			ok:   false,
		},
		{
			name: "plain tmp is rejected",
			path: "/tmp/gbash-cli",
			ok:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := darwinVarFoldersTempRoot(tc.path)
			if ok != tc.ok {
				t.Fatalf("darwinVarFoldersTempRoot(%q) ok = %t, want %t", tc.path, ok, tc.ok)
			}
			if got != tc.want {
				t.Fatalf("darwinVarFoldersTempRoot(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}
