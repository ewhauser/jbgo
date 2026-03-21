package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

func decodeManifest(data []byte) (*manifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var out manifest
	if err := decoder.Decode(&out); err != nil {
		return nil, err
	}

	normalizedXFails := make(map[string]xfailEntry, len(out.ExpectedFailures))
	for rawPath, entry := range out.ExpectedFailures {
		path := normalizeManifestTestPath(rawPath)
		if path == "" {
			return nil, fmt.Errorf("expected failure path must not be empty")
		}
		entry.Reason = strings.TrimSpace(entry.Reason)
		if entry.Reason == "" {
			return nil, fmt.Errorf("expected failure reason must not be empty for %s", path)
		}
		if len(entry.GOOS) > 0 {
			for i, goos := range entry.GOOS {
				goos = strings.TrimSpace(strings.ToLower(goos))
				if goos == "" {
					return nil, fmt.Errorf("expected failure goos must not be empty for %s", path)
				}
				entry.GOOS[i] = goos
			}
		}
		if _, exists := normalizedXFails[path]; exists {
			return nil, fmt.Errorf("duplicate expected failure path %s", path)
		}
		normalizedXFails[path] = entry
	}
	out.ExpectedFailures = normalizedXFails

	return &out, nil
}

func lookupExpectedFailure(mf *manifest, path string) (xfailEntry, bool) {
	if mf == nil {
		return xfailEntry{}, false
	}
	entry, ok := mf.ExpectedFailures[normalizeManifestTestPath(path)]
	if !ok || !xfailEntryMatchesGOOS(entry) {
		return xfailEntry{}, false
	}
	return entry, true
}

func normalizeManifestTestPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, "\\", "/")
	return filepath.ToSlash(path)
}

func xfailEntryMatchesGOOS(entry xfailEntry) bool {
	if len(entry.GOOS) == 0 {
		return true
	}
	return slices.Contains(entry.GOOS, runtime.GOOS)
}
