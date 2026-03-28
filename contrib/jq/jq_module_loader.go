package jq

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/ewhauser/gbash/commands"
	"github.com/itchyny/gojq"
)

type sandboxJQModuleLoader struct {
	inv      *commands.Invocation
	paths    []string
	readFile func(name string) ([]byte, error)
	stat     func(name string) error
}

func newSandboxJQModuleLoader(ctx context.Context, inv *commands.Invocation, paths []string) gojq.ModuleLoader {
	resolved := make([]string, 0, len(paths))
	for _, value := range paths {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		resolved = append(resolved, resolveSandboxJQPath(inv, inv.Cwd, value))
	}
	return &sandboxJQModuleLoader{
		inv:   inv,
		paths: resolved,
		readFile: func(name string) ([]byte, error) {
			return readJQFile(ctx, inv, name)
		},
		stat: func(name string) error {
			_, err := inv.FS.StatQuiet(ctx, name)
			return err
		},
	}
}

func (l *sandboxJQModuleLoader) LoadModule(name string) (*gojq.Query, error) {
	return l.LoadModuleWithMeta(name, nil)
}

func (l *sandboxJQModuleLoader) LoadModuleWithMeta(name string, meta map[string]any) (*gojq.Query, error) {
	modulePath, err := l.lookupModule(name, ".jq", meta)
	if err != nil {
		return nil, err
	}
	data, err := l.readFile(modulePath)
	if err != nil {
		return nil, err
	}
	query, err := gojq.Parse(string(data))
	if err != nil {
		return nil, err
	}
	moduleDir := path.Dir(modulePath)
	for _, importSpec := range query.Imports {
		if importSpec.Meta == nil {
			continue
		}
		for _, keyValue := range importSpec.Meta.KeyVals {
			key := keyValue.Key
			if key == "" {
				key = keyValue.KeyString
			}
			if key != "search" || keyValue.Val == nil {
				continue
			}
			if keyValue.Val.Object != nil || keyValue.Val.Array != nil || keyValue.Val.Number != "" || keyValue.Val.Null || keyValue.Val.True || keyValue.Val.False {
				keyValue.Val = &gojq.ConstTerm{Null: true}
				continue
			}
			keyValue.Val.Str = resolveSandboxJQPath(l.inv, moduleDir, keyValue.Val.Str)
		}
	}
	return query, nil
}

func (l *sandboxJQModuleLoader) LoadJSON(name string) (any, error) {
	return l.LoadJSONWithMeta(name, nil)
}

func (l *sandboxJQModuleLoader) LoadJSONWithMeta(name string, meta map[string]any) (any, error) {
	modulePath, err := l.lookupModule(name, ".json", meta)
	if err != nil {
		return nil, err
	}
	data, err := l.readFile(modulePath)
	if err != nil {
		return nil, err
	}
	values, err := decodeJQJSON(data)
	if err != nil {
		return nil, err
	}
	return values, nil
}

func (l *sandboxJQModuleLoader) lookupModule(name, ext string, meta map[string]any) (string, error) {
	searchPaths := append([]string(nil), l.paths...)
	if rawSearch, ok := meta["search"].(string); ok {
		if resolved := resolveSandboxJQPath(l.inv, "", rawSearch); resolved != "" {
			searchPaths = append([]string{resolved}, searchPaths...)
		}
	}
	for _, base := range searchPaths {
		for _, candidate := range []string{
			path.Join(base, name+ext),
			path.Join(base, name, path.Base(name)+ext),
		} {
			if err := l.stat(candidate); err == nil {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("module not found: %q", name)
}

func resolveSandboxJQPath(inv *commands.Invocation, baseDir, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "/") {
		return path.Clean(value)
	}
	base := strings.TrimSpace(baseDir)
	if base == "" && inv != nil {
		base = inv.Cwd
	}
	if base == "" {
		base = "/"
	}
	return path.Clean(path.Join(base, value))
}
