package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
)

//nolint:forbidigo // Conformance metadata is loaded from checked-in host files during tests.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, err
	}
	if manifest.Suites == nil {
		manifest.Suites = make(map[string]ManifestSuite)
	}
	for suiteName, suite := range manifest.Suites {
		if strings.TrimSpace(suiteName) == "" {
			return nil, fmt.Errorf("manifest suite name must not be empty")
		}
		if suite.Entries == nil {
			suite.Entries = make(map[string]ManifestEntry)
		}
		for key, entry := range suite.Entries {
			if strings.TrimSpace(key) == "" {
				return nil, fmt.Errorf("manifest entry key must not be empty for suite %s", suiteName)
			}
			if !entry.Mode.valid() {
				return nil, fmt.Errorf("invalid manifest mode %q for suite %s key %s", entry.Mode, suiteName, key)
			}
			if strings.TrimSpace(entry.Reason) == "" {
				return nil, fmt.Errorf("manifest reason must not be empty for suite %s key %s", suiteName, key)
			}
			if len(entry.GOOS) > 0 {
				for i, goos := range entry.GOOS {
					goos = strings.TrimSpace(strings.ToLower(goos))
					if goos == "" {
						return nil, fmt.Errorf("manifest goos must not be empty for suite %s key %s", suiteName, key)
					}
					entry.GOOS[i] = goos
				}
			}
			suite.Entries[key] = entry
		}
		manifest.Suites[suiteName] = suite
	}
	return &manifest, nil
}

func (m *Manifest) Lookup(suiteName, filePath, caseName string) (ManifestEntry, bool) {
	if strings.TrimSpace(caseName) != "" {
		return m.LookupCase(suiteName, filePath, caseName)
	}
	return m.LookupFile(suiteName, filePath)
}

func (m *Manifest) LookupFile(suiteName, filePath string) (ManifestEntry, bool) {
	if m == nil {
		return ManifestEntry{}, false
	}
	suite, ok := m.Suites[strings.TrimSpace(suiteName)]
	if !ok {
		return ManifestEntry{}, false
	}
	filePath = normalizeKey(filePath)
	entry, ok := suite.Entries[filePath]
	if ok && !entryMatchesGOOS(entry) {
		return ManifestEntry{}, false
	}
	return entry, ok
}

func (m *Manifest) LookupCase(suiteName, filePath, caseName string) (ManifestEntry, bool) {
	if m == nil {
		return ManifestEntry{}, false
	}
	suite, ok := m.Suites[strings.TrimSpace(suiteName)]
	if !ok {
		return ManifestEntry{}, false
	}
	filePath = normalizeKey(filePath)
	caseKey := filePath + "::" + strings.TrimSpace(caseName)
	entry, ok := suite.Entries[caseKey]
	if ok && !entryMatchesGOOS(entry) {
		return ManifestEntry{}, false
	}
	return entry, ok
}

func entryMatchesGOOS(entry ManifestEntry) bool {
	if len(entry.GOOS) == 0 {
		return true
	}
	return slices.Contains(entry.GOOS, runtime.GOOS)
}

func DetermineCaseOutcome(fileEntry ManifestEntry, hasFileEntry bool, caseEntry ManifestEntry, hasCaseEntry, matched bool) CaseOutcome {
	if hasCaseEntry {
		return determineEntryOutcome(caseEntry, matched)
	}
	if hasFileEntry && fileEntry.Mode == EntryModeSkip {
		return CaseOutcomeSkip
	}
	if matched {
		return CaseOutcomePass
	}
	if hasFileEntry && fileEntry.Mode == EntryModeXFail {
		return CaseOutcomeExpectedFailure
	}
	return CaseOutcomeFailure
}

func determineEntryOutcome(entry ManifestEntry, matched bool) CaseOutcome {
	switch entry.Mode {
	case EntryModeSkip:
		return CaseOutcomeSkip
	case EntryModeXFail:
		if matched {
			return CaseOutcomeUnexpectedPass
		}
		return CaseOutcomeExpectedFailure
	default:
		if matched {
			return CaseOutcomePass
		}
		return CaseOutcomeFailure
	}
}
