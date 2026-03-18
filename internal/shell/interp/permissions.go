// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"context"
	"fmt"
	"io/fs"
	"reflect"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/internal/shell/syntax"
)

// access is similar to checking permission bits from [fs.FileInfo], but it uses
// the runner's virtual identity instead of the host process identity.
func (r *Runner) access(ctx context.Context, name string, mode uint32) error {
	info, err := r.lstat(ctx, name)
	if err != nil {
		return err
	}
	if mode == 0 {
		return nil
	}
	mask := fs.FileMode(0)
	switch mode {
	case access_R_OK:
		mask = 0o4
	case access_W_OK:
		mask = 0o2
	case access_X_OK:
		mask = 0o1
	default:
		return fmt.Errorf("unsupported access mode: %d", mode)
	}
	if fileHasPermission(info, r.euid, r.egid, mask) {
		return nil
	}
	return fmt.Errorf("permission denied")
}

// unTestOwnOrGrp implements the -O and -G unary tests without consulting host
// account databases.
func (r *Runner) unTestOwnOrGrp(ctx context.Context, op syntax.UnTestOperator, x string) bool {
	info, err := r.stat(ctx, x)
	if err != nil {
		return false
	}
	uid, gid, ok := fileOwnerIDs(info)
	if !ok {
		return false
	}
	if op == syntax.TsUsrOwn {
		return uid == r.euid
	}
	return gid == r.egid
}

func fileHasPermission(info fs.FileInfo, currentUID, currentGID int, mask fs.FileMode) bool {
	mode := info.Mode().Perm()
	ownerUID, ownerGID, ok := fileOwnerIDs(info)
	if !ok {
		ownerUID = currentUID
		ownerGID = currentGID
	}
	switch {
	case currentUID == ownerUID:
		return mode&(mask<<6) != 0
	case currentGID == ownerGID:
		return mode&(mask<<3) != 0
	default:
		return mode&mask != 0
	}
}

func fileOwnerIDs(info fs.FileInfo) (uid, gid int, ok bool) {
	if ownership, ok := gbfs.OwnershipFromFileInfo(info); ok {
		return int(ownership.UID), int(ownership.GID), true
	}
	sys := reflect.ValueOf(info.Sys())
	if !sys.IsValid() {
		return 0, 0, false
	}
	if sys.Kind() == reflect.Pointer {
		if sys.IsNil() {
			return 0, 0, false
		}
		sys = sys.Elem()
	}
	if sys.Kind() != reflect.Struct {
		return 0, 0, false
	}
	uidField := sys.FieldByName("Uid")
	gidField := sys.FieldByName("Gid")
	if !uidField.IsValid() || !gidField.IsValid() {
		return 0, 0, false
	}
	return int(reflectUint(uidField)), int(reflectUint(gidField)), true
}

func reflectUint(field reflect.Value) uint64 {
	switch field.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return field.Uint()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return uint64(field.Int())
	default:
		return 0
	}
}
