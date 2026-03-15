package builtins_test

import (
	"context"
	"testing"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/policy"
)

func TestRGUsesIndexedPrefilterOnSearchableFS(t *testing.T) {
	fsys, provider := newCountedSearchableFS(t, map[string]string{
		"/workspace/hit.txt":  "needle\n",
		"/workspace/miss.txt": "other\n",
	})
	session := newSession(t, &Config{
		FileSystem: CustomFileSystem(factoryForFS(fsys), "/workspace"),
	})

	result := mustExecSession(t, session, "rg needle /workspace\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/workspace/hit.txt:1:needle\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if provider.SearchCount() == 0 {
		t.Fatal("SearchCount = 0, want indexed query")
	}
	if got := fsys.OpenCount("/workspace/hit.txt"); got == 0 {
		t.Fatal("OpenCount(hit) = 0, want verification read")
	}
	if got := fsys.OpenCount("/workspace/miss.txt"); got != 0 {
		t.Fatalf("OpenCount(miss) = %d, want 0", got)
	}
}

func TestRGSingleExplicitFileKeepsNoFilenameOnIndexedPath(t *testing.T) {
	fsys, provider := newCountedSearchableFS(t, map[string]string{
		"/workspace/hit.txt": "needle\n",
	})
	session := newSession(t, &Config{
		FileSystem: CustomFileSystem(factoryForFS(fsys), "/workspace"),
	})

	result := mustExecSession(t, session, "rg needle /workspace/hit.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "needle\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if provider.SearchCount() == 0 {
		t.Fatal("SearchCount = 0, want indexed query")
	}
}

func TestRGInlineIgnoreCaseFlagUsesIndexedPrefilter(t *testing.T) {
	fsys, provider := newCountedSearchableFS(t, map[string]string{
		"/workspace/hit.txt": "needle\n",
	})
	session := newSession(t, &Config{
		FileSystem: CustomFileSystem(factoryForFS(fsys), "/workspace"),
	})

	result := mustExecSession(t, session, "rg '(?i)Needle' /workspace/hit.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "needle\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if provider.SearchCount() == 0 {
		t.Fatal("SearchCount = 0, want indexed query")
	}
	if got := fsys.OpenCount("/workspace/hit.txt"); got == 0 {
		t.Fatal("OpenCount(hit) = 0, want verification read")
	}
}

func TestRGUsesIndexPerRootOnMountableFS(t *testing.T) {
	indexedFS, indexedProvider := newCountedSearchableFS(t, map[string]string{
		"/hit.txt":  "needle\n",
		"/miss.txt": "other\n",
	})
	staleFS := newCountedSearchCapableFS(t, map[string]string{
		"/hit.txt":  "needle\n",
		"/miss.txt": "other\n",
	}, grepStaleProvider{})

	mountable := gbfs.NewMountable(gbfs.NewMemory())
	if err := mountable.Mount("/indexed", indexedFS); err != nil {
		t.Fatalf("Mount(/indexed) error = %v", err)
	}
	if err := mountable.Mount("/stale", staleFS); err != nil {
		t.Fatalf("Mount(/stale) error = %v", err)
	}

	session := newSession(t, &Config{
		FileSystem: CustomFileSystem(factoryForFS(mountable), "/"),
	})

	result := mustExecSession(t, session, "rg needle /indexed /stale\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	for _, want := range []string{"/indexed/hit.txt:1:needle", "/stale/hit.txt:1:needle"} {
		if !containsLine(lines(result.Stdout), want) {
			t.Fatalf("Stdout missing %q: %q", want, result.Stdout)
		}
	}
	if indexedProvider.SearchCount() == 0 {
		t.Fatal("indexed SearchCount = 0, want indexed query")
	}
	if got := indexedFS.OpenCount("/hit.txt"); got == 0 {
		t.Fatal("indexed hit open count = 0, want verification read")
	}
	if got := indexedFS.OpenCount("/miss.txt"); got != 0 {
		t.Fatalf("indexed miss open count = %d, want 0", got)
	}
	if got := staleFS.OpenCount("/miss.txt"); got == 0 {
		t.Fatal("stale miss open count = 0, want fallback read")
	}
}

func TestRGFallsBackWhenProviderIsUnsupported(t *testing.T) {
	provider := &grepUnsupportedProvider{}
	fsys := newCountedSearchCapableFS(t, map[string]string{
		"/workspace/miss.txt": "other\n",
	}, provider)
	session := newSession(t, &Config{
		FileSystem: CustomFileSystem(factoryForFS(fsys), "/workspace"),
	})

	result := mustExecSession(t, session, "rg -s needle /workspace/miss.txt\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if provider.SearchCount() == 0 {
		t.Fatal("SearchCount = 0, want attempted provider search")
	}
	if got := fsys.OpenCount("/workspace/miss.txt"); got == 0 {
		t.Fatal("OpenCount(miss) = 0, want fallback read")
	}
}

func TestRGFallsBackWhenProviderIsMissing(t *testing.T) {
	fsys := newCountingOpenFS(seededMemoryFS(t, map[string]string{
		"/workspace/miss.txt": "other\n",
	}))
	session := newSession(t, &Config{
		FileSystem: CustomFileSystem(factoryForFS(fsys), "/workspace"),
	})

	result := mustExecSession(t, session, "rg needle /workspace/miss.txt\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if got := fsys.OpenCount("/workspace/miss.txt"); got == 0 {
		t.Fatal("OpenCount(miss) = 0, want fallback read")
	}
}

func TestRGInvertMatchSkipsIndexedPrefilter(t *testing.T) {
	fsys, provider := newCountedSearchableFS(t, map[string]string{
		"/workspace/miss.txt": "other\n",
	})
	session := newSession(t, &Config{
		FileSystem: CustomFileSystem(factoryForFS(fsys), "/workspace"),
	})

	result := mustExecSession(t, session, "rg -v needle /workspace/miss.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "other\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if provider.SearchCount() != 0 {
		t.Fatalf("SearchCount = %d, want 0", provider.SearchCount())
	}
	if got := fsys.OpenCount("/workspace/miss.txt"); got == 0 {
		t.Fatal("OpenCount(miss) = 0, want direct read")
	}
}

func TestRGPatternWithoutSafeLiteralFallsBack(t *testing.T) {
	fsys, provider := newCountedSearchableFS(t, map[string]string{
		"/workspace/miss.txt": "other\n",
	})
	session := newSession(t, &Config{
		FileSystem: CustomFileSystem(factoryForFS(fsys), "/workspace"),
	})

	result := mustExecSession(t, session, "rg '[0-9]+' /workspace/miss.txt\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if provider.SearchCount() != 0 {
		t.Fatalf("SearchCount = %d, want 0", provider.SearchCount())
	}
	if got := fsys.OpenCount("/workspace/miss.txt"); got == 0 {
		t.Fatal("OpenCount(miss) = 0, want fallback read")
	}
}

func TestRGFollowedSymlinkDirectoryFallsBackToDirectReads(t *testing.T) {
	fsys, _ := newCountedSearchableFS(t, map[string]string{
		"/workspace/real/miss.txt": "other\n",
	})
	session := newSession(t, &Config{
		FileSystem: CustomFileSystem(factoryForFS(fsys), "/workspace"),
		Policy: policy.NewStatic(&policy.Config{
			SymlinkMode: policy.SymlinkFollow,
		}),
	})
	if err := session.FileSystem().Symlink(context.Background(), "real", "/workspace/linkdir"); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	result := mustExecSession(t, session, "rg -L needle /workspace/linkdir\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if got := fsys.OpenCount("/workspace/linkdir/miss.txt"); got == 0 {
		t.Fatal("OpenCount(linkdir/miss.txt) = 0, want fallback read")
	}
}

func TestRGBinaryGuaranteedMissModesPreserveParity(t *testing.T) {
	tests := []struct {
		name       string
		script     string
		wantStdout string
		wantExit   int
		wantOpen   int
	}{
		{
			name:       "count-without-text",
			script:     "rg -c needle /workspace/miss.bin\n",
			wantStdout: "",
			wantExit:   1,
			wantOpen:   1,
		},
		{
			name:       "files-without-match-without-text",
			script:     "rg --files-without-match needle /workspace/miss.bin\n",
			wantStdout: "",
			wantExit:   1,
			wantOpen:   1,
		},
		{
			name:       "count-with-text",
			script:     "rg -a -c needle /workspace/miss.bin\n",
			wantStdout: "0\n",
			wantExit:   1,
			wantOpen:   0,
		},
		{
			name:       "files-without-match-with-text",
			script:     "rg -a --files-without-match needle /workspace/miss.bin\n",
			wantStdout: "/workspace/miss.bin\n",
			wantExit:   0,
			wantOpen:   0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fsys, provider := newCountedSearchableFS(t, map[string]string{
				"/workspace/miss.bin": "other\x00world\n",
			})
			session := newSession(t, &Config{
				FileSystem: CustomFileSystem(factoryForFS(fsys), "/workspace"),
			})

			result := mustExecSession(t, session, tc.script)
			if result.ExitCode != tc.wantExit {
				t.Fatalf("ExitCode = %d, want %d; stderr=%q", result.ExitCode, tc.wantExit, result.Stderr)
			}
			if got := result.Stdout; got != tc.wantStdout {
				t.Fatalf("Stdout = %q, want %q", got, tc.wantStdout)
			}
			if provider.SearchCount() == 0 {
				t.Fatal("SearchCount = 0, want indexed query")
			}
			if got := fsys.OpenCount("/workspace/miss.bin"); got != tc.wantOpen {
				t.Fatalf("OpenCount(miss.bin) = %d, want %d", got, tc.wantOpen)
			}
		})
	}
}

func TestRGQuietUsesIndexedPrefilterOnHit(t *testing.T) {
	fsys, provider := newCountedSearchableFS(t, map[string]string{
		"/workspace/hit.txt": "needle\n",
	})
	session := newSession(t, &Config{
		FileSystem: CustomFileSystem(factoryForFS(fsys), "/workspace"),
	})

	result := mustExecSession(t, session, "rg -q needle /workspace/hit.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if result.Stdout != "" {
		t.Fatalf("Stdout = %q, want empty", result.Stdout)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
	if provider.SearchCount() == 0 {
		t.Fatal("SearchCount = 0, want indexed query")
	}
}
