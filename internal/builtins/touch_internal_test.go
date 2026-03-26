package builtins

import (
	"slices"
	"testing"
)

func TestTouchNormalizeInvocationKeepsTrailingDoubleDashOperands(t *testing.T) {
	t.Parallel()

	cmd := NewTouch()
	inv := &Invocation{
		Args: []string{"01010000", "/tmp/out.txt", "--"},
		Env:  map[string]string{"_POSIX2_VERSION": "199209"},
	}

	got := cmd.NormalizeInvocation(inv)
	want := []string{"--posix-stamp", "01010000", "/tmp/out.txt", "--"}
	if got == nil || !slices.Equal(got.Args, want) {
		t.Fatalf("NormalizeInvocation() args = %#v, want %#v", got.Args, want)
	}
}

func TestTouchNormalizeInvocationConsumesInferredLongOptionValues(t *testing.T) {
	t.Parallel()

	cmd := NewTouch()
	inv := &Invocation{
		Args: []string{"--ti", "mtime", "01010000", "/tmp/out.txt"},
		Env:  map[string]string{"_POSIX2_VERSION": "199209"},
	}

	got := cmd.NormalizeInvocation(inv)
	want := []string{"--ti", "mtime", "--posix-stamp", "01010000", "/tmp/out.txt"}
	if got == nil || !slices.Equal(got.Args, want) {
		t.Fatalf("NormalizeInvocation() args = %#v, want %#v", got.Args, want)
	}
}
