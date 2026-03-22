package host

import (
	"slices"
	"testing"
)

func TestOSPlatformDefaultsWindows(t *testing.T) {
	t.Parallel()

	defaults := OSWindows.PlatformDefaults()
	if !defaults.EnvCaseInsensitive {
		t.Fatal("EnvCaseInsensitive = false, want true")
	}
	if defaults.RequireExecutableBit {
		t.Fatal("RequireExecutableBit = true, want false")
	}
	if got, want := defaults.OSType, "msys"; got != want {
		t.Fatalf("OSType = %q, want %q", got, want)
	}
	if got, want := defaults.KernelName, "Windows_NT"; got != want {
		t.Fatalf("KernelName = %q, want %q", got, want)
	}
	if got, want := defaults.OperatingSystem, "MS/Windows"; got != want {
		t.Fatalf("OperatingSystem = %q, want %q", got, want)
	}
	if got, want := defaults.PathExtensions, []string{".com", ".exe", ".bat", ".cmd"}; !slices.Equal(got, want) {
		t.Fatalf("PathExtensions = %v, want %v", got, want)
	}
}

func TestOSPlatformDefaultsLinux(t *testing.T) {
	t.Parallel()

	defaults := OSLinux.PlatformDefaults()
	if defaults.EnvCaseInsensitive {
		t.Fatal("EnvCaseInsensitive = true, want false")
	}
	if !defaults.RequireExecutableBit {
		t.Fatal("RequireExecutableBit = false, want true")
	}
	if got, want := defaults.OSType, "linux-gnu"; got != want {
		t.Fatalf("OSType = %q, want %q", got, want)
	}
	if got, want := defaults.KernelName, "Linux"; got != want {
		t.Fatalf("KernelName = %q, want %q", got, want)
	}
	if got, want := defaults.OperatingSystem, "GNU/Linux"; got != want {
		t.Fatalf("OperatingSystem = %q, want %q", got, want)
	}
	if len(defaults.PathExtensions) != 0 {
		t.Fatalf("PathExtensions len = %d, want 0", len(defaults.PathExtensions))
	}
}

func TestOSPlatformDefaultsUnknown(t *testing.T) {
	t.Parallel()

	defaults := OS("myos").PlatformDefaults()
	if got, want := defaults.OSType, "myos"; got != want {
		t.Fatalf("OSType = %q, want %q", got, want)
	}
	if got, want := defaults.KernelName, "myos"; got != want {
		t.Fatalf("KernelName = %q, want %q", got, want)
	}
	if got, want := defaults.OperatingSystem, "myos"; got != want {
		t.Fatalf("OperatingSystem = %q, want %q", got, want)
	}
	if !defaults.RequireExecutableBit {
		t.Fatal("RequireExecutableBit = false, want true")
	}
}
