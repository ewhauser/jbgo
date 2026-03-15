package builtins

import (
	"context"
	stdfs "io/fs"
	"testing"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
)

type permissionDeniedIdentityFS struct{}

func (permissionDeniedIdentityFS) Open(context.Context, string) (gbfs.File, error) {
	return nil, stdfs.ErrPermission
}

func (permissionDeniedIdentityFS) OpenFile(context.Context, string, int, stdfs.FileMode) (gbfs.File, error) {
	return nil, stdfs.ErrPermission
}

func (permissionDeniedIdentityFS) Stat(context.Context, string) (stdfs.FileInfo, error) {
	return nil, stdfs.ErrPermission
}

func (permissionDeniedIdentityFS) Lstat(context.Context, string) (stdfs.FileInfo, error) {
	return nil, stdfs.ErrPermission
}

func (permissionDeniedIdentityFS) ReadDir(context.Context, string) ([]stdfs.DirEntry, error) {
	return nil, stdfs.ErrPermission
}

func (permissionDeniedIdentityFS) Readlink(context.Context, string) (string, error) {
	return "", stdfs.ErrPermission
}

func (permissionDeniedIdentityFS) Realpath(context.Context, string) (string, error) {
	return "", stdfs.ErrPermission
}

func (permissionDeniedIdentityFS) Symlink(context.Context, string, string) error {
	return stdfs.ErrPermission
}

func (permissionDeniedIdentityFS) Link(context.Context, string, string) error {
	return stdfs.ErrPermission
}

func (permissionDeniedIdentityFS) Chown(context.Context, string, uint32, uint32, bool) error {
	return stdfs.ErrPermission
}

func (permissionDeniedIdentityFS) Chmod(context.Context, string, stdfs.FileMode) error {
	return stdfs.ErrPermission
}

func (permissionDeniedIdentityFS) Chtimes(context.Context, string, time.Time, time.Time) error {
	return stdfs.ErrPermission
}

func (permissionDeniedIdentityFS) MkdirAll(context.Context, string, stdfs.FileMode) error {
	return stdfs.ErrPermission
}

func (permissionDeniedIdentityFS) Remove(context.Context, string, bool) error {
	return stdfs.ErrPermission
}

func (permissionDeniedIdentityFS) Rename(context.Context, string, string) error {
	return stdfs.ErrPermission
}

func (permissionDeniedIdentityFS) Getwd() string {
	return "/"
}

func (permissionDeniedIdentityFS) Chdir(string) error {
	return stdfs.ErrPermission
}

func TestLoadPermissionIdentityDBDoesNotFallbackToHostFiles(t *testing.T) {
	inv := NewInvocation(&InvocationOptions{
		Cwd:        "/",
		FileSystem: permissionDeniedIdentityFS{},
	})
	db := &permissionIdentityDB{
		usersByName:  make(map[string]uint32),
		usersByID:    make(map[uint32]string),
		groupsByName: make(map[string]uint32),
		groupsByID:   make(map[uint32]string),
	}

	loadPermissionPasswd(context.Background(), inv, db)
	loadPermissionGroup(context.Background(), inv, db)

	if len(db.usersByName) != 0 || len(db.usersByID) != 0 || len(db.groupsByName) != 0 || len(db.groupsByID) != 0 {
		t.Fatalf("identity DB = %#v, want no host fallback entries", db)
	}
}

func TestSeedPermissionIdentityDBFromEnvDoesNotInjectHostIdentity(t *testing.T) {
	db := &permissionIdentityDB{
		usersByName:  make(map[string]uint32),
		usersByID:    make(map[uint32]string),
		groupsByName: make(map[string]uint32),
		groupsByID:   make(map[uint32]string),
	}
	inv := NewInvocation(&InvocationOptions{
		Env: map[string]string{
			"USER":  "sandbox-user",
			"GROUP": "sandbox-group",
			"UID":   "4242",
			"GID":   "4343",
		},
	})

	seedPermissionIdentityDBFromEnv(db, inv)

	if got, want := len(db.usersByName), 1; got != want {
		t.Fatalf("len(usersByName) = %d, want %d; db=%#v", got, want, db)
	}
	if got, want := len(db.usersByID), 1; got != want {
		t.Fatalf("len(usersByID) = %d, want %d; db=%#v", got, want, db)
	}
	if got, want := len(db.groupsByName), 1; got != want {
		t.Fatalf("len(groupsByName) = %d, want %d; db=%#v", got, want, db)
	}
	if got, want := len(db.groupsByID), 1; got != want {
		t.Fatalf("len(groupsByID) = %d, want %d; db=%#v", got, want, db)
	}
	if got, want := db.usersByName["sandbox-user"], uint32(4242); got != want {
		t.Fatalf("usersByName[sandbox-user] = %d, want %d", got, want)
	}
	if got, want := db.usersByID[4242], "sandbox-user"; got != want {
		t.Fatalf("usersByID[4242] = %q, want %q", got, want)
	}
	if got, want := db.groupsByName["sandbox-group"], uint32(4343); got != want {
		t.Fatalf("groupsByName[sandbox-group] = %d, want %d", got, want)
	}
	if got, want := db.groupsByID[4343], "sandbox-group"; got != want {
		t.Fatalf("groupsByID[4343] = %q, want %q", got, want)
	}
}
