package builtins

import (
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"path"
	"strconv"
	"strings"

	gbfs "github.com/ewhauser/gbash/fs"
)

type permissionVerbosityLevel uint8

const (
	permissionVerbosityNormal permissionVerbosityLevel = iota
	permissionVerbositySilent
	permissionVerbosityChanges
	permissionVerbosityVerbose
)

type permissionVerbosity struct {
	level      permissionVerbosityLevel
	groupsOnly bool
}

type permissionTraverseSymlinks uint8

const (
	permissionTraverseNone permissionTraverseSymlinks = iota
	permissionTraverseFirst
	permissionTraverseAll
)

type permissionWalkOptions struct {
	commandName      string
	recursive        bool
	preserveRoot     bool
	dereference      bool
	traverseSymlinks permissionTraverseSymlinks
}

type permissionVisit struct {
	Abs    string
	Info   stdfs.FileInfo
	Follow bool
}

type permissionIdentityDB struct {
	usersByName  map[string]uint32
	usersByID    map[uint32]string
	groupsByName map[string]uint32
	groupsByID   map[uint32]string
}

type permissionOwnership struct {
	uid   uint32
	gid   uint32
	user  string
	group string
}

type permissionIfFrom struct {
	setUID bool
	uid    uint32
	setGID bool
	gid    uint32
}

func normalizePermissionWalkOptionsForCommand(inv *Invocation, commandName string, recursive bool, dereference *bool, traverse permissionTraverseSymlinks, preserveRoot bool) (permissionWalkOptions, error) {
	follow := true
	if dereference != nil {
		follow = *dereference
	}
	if !recursive {
		traverse = permissionTraverseNone
	}
	if recursive && traverse == permissionTraverseNone {
		if dereference != nil && *dereference {
			return permissionWalkOptions{}, exitf(inv, 1, "%s: -R --dereference requires -H or -L", commandName)
		}
		follow = false
	}
	return permissionWalkOptions{
		commandName:      commandName,
		recursive:        recursive,
		preserveRoot:     preserveRoot,
		dereference:      follow,
		traverseSymlinks: traverse,
	}, nil
}

func loadPermissionIdentityDB(ctx context.Context, inv *Invocation) *permissionIdentityDB {
	db := &permissionIdentityDB{
		usersByName:  make(map[string]uint32),
		usersByID:    make(map[uint32]string),
		groupsByName: make(map[string]uint32),
		groupsByID:   make(map[uint32]string),
	}
	seedPermissionIdentityDBFromEnv(db, inv)
	loadPermissionPasswd(ctx, inv, db)
	loadPermissionGroup(ctx, inv, db)
	return db
}

func seedPermissionIdentityDBFromEnv(db *permissionIdentityDB, inv *Invocation) {
	if db == nil || inv == nil {
		return
	}
	uid := uint32(idUintEnv(inv.Env, "UID", idDefaultUID))
	gid := uint32(idUintEnv(inv.Env, "GID", idDefaultGID))
	user := strings.TrimSpace(inv.Env["USER"])
	if user == "" {
		user = idDefaultUserName
	}
	group := strings.TrimSpace(inv.Env["GROUP"])
	if group == "" {
		group = user
	}
	db.usersByName[user] = uid
	if _, ok := db.usersByID[uid]; !ok {
		db.usersByID[uid] = user
	}
	db.groupsByName[group] = gid
	if _, ok := db.groupsByID[gid]; !ok {
		db.groupsByID[gid] = group
	}
}

func loadPermissionPasswd(ctx context.Context, inv *Invocation, db *permissionIdentityDB) {
	input, _, err := openRead(ctx, inv, "/etc/passwd")
	if err != nil {
		return
	}
	defer func() { _ = input.Close() }()
	data, err := readAllReader(ctx, inv, input)
	if err != nil {
		return
	}
	for _, line := range textLines(data) {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) < 4 {
			continue
		}
		uid, err := strconv.ParseUint(fields[2], 10, 32)
		if err != nil {
			continue
		}
		db.usersByName[fields[0]] = uint32(uid)
		if _, ok := db.usersByID[uint32(uid)]; !ok {
			db.usersByID[uint32(uid)] = fields[0]
		}
	}
}

func loadPermissionGroup(ctx context.Context, inv *Invocation, db *permissionIdentityDB) {
	input, _, err := openRead(ctx, inv, "/etc/group")
	if err != nil {
		return
	}
	defer func() { _ = input.Close() }()
	data, err := readAllReader(ctx, inv, input)
	if err != nil {
		return
	}
	for _, line := range textLines(data) {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) < 3 {
			continue
		}
		gid, err := strconv.ParseUint(fields[2], 10, 32)
		if err != nil {
			continue
		}
		db.groupsByName[fields[0]] = uint32(gid)
		if _, ok := db.groupsByID[uint32(gid)]; !ok {
			db.groupsByID[uint32(gid)] = fields[0]
		}
	}
}

func permissionLookupOwnership(db *permissionIdentityDB, info stdfs.FileInfo) permissionOwnership {
	meta, ok := gbfs.OwnershipFromFileInfo(info)
	if !ok {
		meta = gbfs.DefaultOwnership()
	}
	owner := permissionOwnership{
		uid: meta.UID,
		gid: meta.GID,
	}
	if db != nil {
		owner.user = db.usersByID[owner.uid]
		owner.group = db.groupsByID[owner.gid]
	}
	return owner
}

func parsePermissionOwnerSpec(inv *Invocation, db *permissionIdentityDB, spec string) (uid, gid *uint32, err error) {
	return parsePermissionOwnerSpecForCommand(inv, db, spec, "chown")
}

func parsePermissionFilterSpec(inv *Invocation, db *permissionIdentityDB, spec string) (permissionIfFrom, error) {
	return parsePermissionFilterSpecForCommand(inv, db, spec, "chown")
}

func parsePermissionOwnerSpecForCommand(inv *Invocation, db *permissionIdentityDB, spec, commandName string) (uid, gid *uint32, err error) {
	return parsePermissionOwnerGroupSpecForCommand(inv, db, spec, true, commandName)
}

func parsePermissionFilterSpecForCommand(inv *Invocation, db *permissionIdentityDB, spec, commandName string) (permissionIfFrom, error) {
	uid, gid, err := parsePermissionOwnerGroupSpecForCommand(inv, db, spec, false, commandName)
	if err != nil {
		return permissionIfFrom{}, err
	}
	filter := permissionIfFrom{}
	if uid != nil {
		filter.setUID = true
		filter.uid = *uid
	}
	if gid != nil {
		filter.setGID = true
		filter.gid = *gid
	}
	return filter, nil
}

func parsePermissionOwnerGroupSpecForCommand(inv *Invocation, db *permissionIdentityDB, spec string, supportDot bool, commandName string) (uid, gid *uint32, err error) {
	if spec == "" {
		return nil, nil, nil
	}
	var sep byte
	switch {
	case strings.Contains(spec, ":"):
		sep = ':'
	case supportDot && strings.Contains(spec, "."):
		sep = '.'
	default:
		uid, err := parsePermissionUserForCommand(inv, db, spec, commandName)
		if err != nil {
			return nil, nil, err
		}
		return &uid, nil, nil
	}
	idx := strings.IndexByte(spec, sep)
	userPart := spec[:idx]
	groupPart := spec[idx+1:]
	if userPart == "" && groupPart == "" {
		return nil, nil, nil
	}
	var uidPtr *uint32
	if userPart != "" {
		uid, err := parsePermissionUserForCommand(inv, db, userPart, commandName)
		if err != nil {
			return nil, nil, err
		}
		uidPtr = &uid
	}
	var gidPtr *uint32
	if groupPart != "" {
		gid, err := parsePermissionGroupForCommand(inv, db, groupPart, commandName)
		if err != nil {
			return nil, nil, err
		}
		gidPtr = &gid
	}
	if groupPart == "" && userPart != "" && startsWithDigit(userPart) && spec != userPart {
		return nil, nil, exitf(inv, 1, "%s: invalid spec: %s", commandName, spec)
	}
	return uidPtr, gidPtr, nil
}

func parsePermissionUserForCommand(inv *Invocation, db *permissionIdentityDB, value, commandName string) (uint32, error) {
	if db != nil {
		if uid, ok := db.usersByName[value]; ok {
			return uid, nil
		}
	}
	uid, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, exitf(inv, 1, "%s: invalid user: %s", commandName, value)
	}
	return uint32(uid), nil
}

func parsePermissionGroupForCommand(inv *Invocation, db *permissionIdentityDB, value, commandName string) (uint32, error) {
	if db != nil {
		if gid, ok := db.groupsByName[value]; ok {
			return gid, nil
		}
	}
	gid, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, exitf(inv, 1, "%s: invalid group: %s", commandName, value)
	}
	return uint32(gid), nil
}

func permissionMatchesFilter(filter permissionIfFrom, owner permissionOwnership) bool {
	if filter.setUID && owner.uid != filter.uid {
		return false
	}
	if filter.setGID && owner.gid != filter.gid {
		return false
	}
	return true
}

func walkPermissionTarget(ctx context.Context, inv *Invocation, target string, opts permissionWalkOptions, visit func(permissionVisit) error) error {
	seen := make(map[string]struct{})
	abs := inv.FS.Resolve(target)
	return walkPermissionPath(ctx, inv, abs, opts, true, seen, visit)
}

func walkPermissionPath(ctx context.Context, inv *Invocation, abs string, opts permissionWalkOptions, root bool, seen map[string]struct{}, visit func(permissionVisit) error) error {
	linfo, err := inv.FS.Lstat(ctx, abs)
	if err != nil {
		return err
	}
	symlink := linfo.Mode()&stdfs.ModeSymlink != 0
	follow := opts.dereference
	if opts.recursive {
		switch opts.traverseSymlinks {
		case permissionTraverseNone:
			follow = false
		case permissionTraverseFirst:
			follow = root && symlink
			if !root {
				follow = false
			}
		case permissionTraverseAll:
			follow = symlink || opts.dereference
		}
	}
	info := linfo
	if follow {
		info, err = inv.FS.Stat(ctx, abs)
		if err != nil {
			return err
		}
	}
	if opts.recursive && opts.preserveRoot {
		resolvedPath, err := inv.FS.Realpath(ctx, abs)
		if err == nil && resolvedPath == "/" {
			return fmt.Errorf("%s: it is dangerous to operate recursively on %q\n%s: use --no-preserve-root to override this failsafe", opts.commandName, abs, opts.commandName)
		}
	}
	if err := visit(permissionVisit{Abs: abs, Info: info, Follow: follow}); err != nil {
		return err
	}
	if !opts.recursive || !info.IsDir() {
		return nil
	}
	if resolvedPath, err := inv.FS.Realpath(ctx, abs); err == nil {
		if _, ok := seen[resolvedPath]; ok {
			return nil
		}
		seen[resolvedPath] = struct{}{}
	}
	entries, err := inv.FS.ReadDir(ctx, abs)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		child := path.Join(abs, entry.Name())
		if err := walkPermissionPath(ctx, inv, child, opts, false, seen, visit); err != nil {
			return err
		}
	}
	return nil
}

func permissionSuccessMessage(targetPath string, before, after permissionOwnership, verbosity permissionVerbosity) string {
	switch {
	case before.uid == after.uid && before.gid == after.gid && verbosity.level == permissionVerbosityVerbose:
		if verbosity.groupsOnly {
			return fmt.Sprintf("group of %q retained as %s", targetPath, permissionNameOrID(after.group, after.gid))
		}
		return fmt.Sprintf("ownership of %q retained as %s:%s", targetPath, permissionNameOrID(after.user, after.uid), permissionNameOrID(after.group, after.gid))
	case before.uid == after.uid && before.gid == after.gid:
		return ""
	case verbosity.level == permissionVerbosityChanges || verbosity.level == permissionVerbosityVerbose:
		if verbosity.groupsOnly {
			return fmt.Sprintf("changed group of %q from %s to %s", targetPath, permissionNameOrID(before.group, before.gid), permissionNameOrID(after.group, after.gid))
		}
		return fmt.Sprintf("changed ownership of %q from %s:%s to %s:%s", targetPath, permissionNameOrID(before.user, before.uid), permissionNameOrID(before.group, before.gid), permissionNameOrID(after.user, after.uid), permissionNameOrID(after.group, after.gid))
	default:
		return ""
	}
}

func permissionFailureMessage(targetPath string, before, after permissionOwnership, verbosity permissionVerbosity, err error) string {
	if verbosity.level == permissionVerbositySilent {
		return ""
	}
	label := "ownership"
	if verbosity.groupsOnly {
		label = "group"
	}
	message := fmt.Sprintf("changing %s of %q: %v", label, targetPath, err)
	if verbosity.level != permissionVerbosityVerbose {
		return message
	}
	if verbosity.groupsOnly {
		return message + "\n" + fmt.Sprintf("failed to change group of %q from %s to %s", targetPath, permissionNameOrID(before.group, before.gid), permissionNameOrID(after.group, after.gid))
	}
	return message + "\n" + fmt.Sprintf("failed to change ownership of %q from %s:%s to %s:%s", targetPath, permissionNameOrID(before.user, before.uid), permissionNameOrID(before.group, before.gid), permissionNameOrID(after.user, after.uid), permissionNameOrID(after.group, after.gid))
}

func permissionNameOrID(name string, id uint32) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	return strconv.FormatUint(uint64(id), 10)
}

type permissionApplyOptions struct {
	commandName string
	files       []string
	uid         *uint32
	gid         *uint32
	filter      permissionIfFrom
	verbosity   permissionVerbosity
	walk        permissionWalkOptions
}

func runPermissionApply(ctx context.Context, inv *Invocation, db *permissionIdentityDB, opts *permissionApplyOptions) error {
	hadError := false
	for _, target := range opts.files {
		err := walkPermissionTarget(ctx, inv, target, opts.walk, func(visit permissionVisit) error {
			before := permissionLookupOwnership(db, visit.Info)
			if !permissionMatchesFilter(opts.filter, before) {
				if message := permissionSuccessMessage(visit.Abs, before, before, opts.verbosity); message != "" {
					_, _ = fmt.Fprintln(inv.Stderr, message)
				}
				return nil
			}

			after := before
			if opts.uid != nil {
				after.uid = *opts.uid
				after.user = db.usersByID[after.uid]
			}
			if opts.gid != nil {
				after.gid = *opts.gid
				after.group = db.groupsByID[after.gid]
			}

			if err := inv.FS.Chown(ctx, visit.Abs, after.uid, after.gid, visit.Follow); err != nil {
				hadError = true
				if message := permissionFailureMessage(visit.Abs, before, after, opts.verbosity, unwrapPermissionError(err)); message != "" {
					_, _ = fmt.Fprintln(inv.Stderr, message)
				}
				return nil
			}

			if message := permissionSuccessMessage(visit.Abs, before, after, opts.verbosity); message != "" {
				_, _ = fmt.Fprintln(inv.Stderr, message)
			}
			return nil
		})
		if err == nil {
			continue
		}
		hadError = true
		if opts.verbosity.level != permissionVerbositySilent {
			_, _ = fmt.Fprintln(inv.Stderr, permissionTargetError(opts.commandName, target, err))
		}
	}

	if hadError {
		return &ExitError{Code: 1}
	}
	return nil
}

func unwrapPermissionError(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) && exitErr.Err != nil {
		return exitErr.Err
	}
	return err
}

func permissionTargetError(commandName, target string, err error) string {
	if err == nil {
		return ""
	}
	err = unwrapPermissionError(err)
	if strings.HasPrefix(err.Error(), commandName+": ") {
		return err.Error()
	}
	switch {
	case errors.Is(err, stdfs.ErrNotExist):
		return fmt.Sprintf("%s: cannot access %q: No such file or directory", commandName, target)
	case errors.Is(err, stdfs.ErrPermission):
		return fmt.Sprintf("%s: cannot access %q: Permission denied", commandName, target)
	default:
		return fmt.Sprintf("%s: cannot access %q: %v", commandName, target, err)
	}
}

func startsWithDigit(value string) bool {
	if value == "" {
		return false
	}
	return value[0] >= '0' && value[0] <= '9'
}
