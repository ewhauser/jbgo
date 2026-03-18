package conformance

import (
	"runtime"
	"testing"
)

func TestLoadManifestAndLookup(t *testing.T) {
	t.Parallel()

	otherGOOS := "linux"
	if runtime.GOOS == "linux" {
		otherGOOS = "darwin"
	}

	path := writeTempFile(t, `{
  "suites": {
    "bash": {
      "entries": {
        "oils/example.test.sh": { "mode": "skip", "reason": "skip file" },
        "oils/example.test.sh::case one": { "mode": "xfail", "reason": "case xfail", "goos": ["`+runtime.GOOS+`"] },
        "oils/example.test.sh::case two": { "mode": "xfail", "reason": "other platform", "goos": ["`+otherGOOS+`"] }
      }
    }
  }
}`)
	manifest, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	entry, ok := manifest.Lookup("bash", "oils/example.test.sh", "case one")
	if !ok || entry.Mode != EntryModeXFail {
		t.Fatalf("Lookup(case) = (%+v, %t), want case xfail", entry, ok)
	}
	if _, ok := manifest.LookupCase("bash", "oils/example.test.sh", "case two"); ok {
		t.Fatalf("LookupCase(other platform) = true, want false")
	}
	entry, ok = manifest.LookupFile("bash", "oils/example.test.sh")
	if !ok || entry.Mode != EntryModeSkip {
		t.Fatalf("LookupFile(file) = (%+v, %t), want file skip", entry, ok)
	}
	if _, ok := manifest.LookupCase("bash", "oils/example.test.sh", "missing"); ok {
		t.Fatalf("LookupCase(missing) = true, want false")
	}
	if _, ok := manifest.Lookup("posix", "oils/example.test.sh", ""); ok {
		t.Fatalf("Lookup(other suite) = true, want false")
	}
}
