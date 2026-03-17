package builtins

import (
	"context"
	"testing"
)

//nolint:paralleltest // Mutates the package-global identity DB loader.
func TestPrimeLSIdentityDBCachesPerInvocation(t *testing.T) {
	t.Parallel()

	calls := 0

	opts := &lsOptions{
		longFormat: true,
		showOwner:  true,
		showGroup:  true,
		identityDBLoader: func(context.Context, *Invocation) *permissionIdentityDB {
			calls++
			return &permissionIdentityDB{}
		},
	}

	primeLSIdentityDB(context.Background(), &Invocation{}, opts)
	primeLSIdentityDB(context.Background(), &Invocation{}, opts)

	if calls != 1 {
		t.Fatalf("loader calls = %d, want 1", calls)
	}
	if opts.identityDB == nil {
		t.Fatal("identityDB = nil, want cached DB")
	}
}

func TestPrimeLSIdentityDBSkipsNumericIDs(t *testing.T) {
	t.Parallel()

	calls := 0

	opts := &lsOptions{
		longFormat: true,
		showOwner:  true,
		showGroup:  true,
		numericIDs: true,
		identityDBLoader: func(context.Context, *Invocation) *permissionIdentityDB {
			calls++
			return &permissionIdentityDB{}
		},
	}

	primeLSIdentityDB(context.Background(), &Invocation{}, opts)

	if calls != 0 {
		t.Fatalf("loader calls = %d, want 0", calls)
	}
	if opts.identityDB != nil {
		t.Fatalf("identityDB = %#v, want nil", opts.identityDB)
	}
}
