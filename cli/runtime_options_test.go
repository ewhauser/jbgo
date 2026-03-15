package cli

import "testing"

func TestInheritedCLIEnvIncludesProcessEnvAndOverrides(t *testing.T) {
	t.Setenv("GBASH_UMASK", "0005")
	t.Setenv("FROM_PROCESS", "present")

	env := inheritedCLIEnv(map[string]string{
		"PATH": "/bin",
	})

	if got, want := env["GBASH_UMASK"], "0005"; got != want {
		t.Fatalf("GBASH_UMASK = %q, want %q", got, want)
	}
	if got, want := env["FROM_PROCESS"], "present"; got != want {
		t.Fatalf("FROM_PROCESS = %q, want %q", got, want)
	}
	if got, want := env["PATH"], "/bin"; got != want {
		t.Fatalf("PATH = %q, want %q", got, want)
	}
}
