package builtins

import (
	"context"
	"testing"
)

func TestPrimeLSIdentityDBCachesPerInvocation(t *testing.T) {
	original := lsIdentityDBLoader
	t.Cleanup(func() {
		lsIdentityDBLoader = original
	})

	calls := 0
	lsIdentityDBLoader = func(context.Context, *Invocation) *permissionIdentityDB {
		calls++
		return &permissionIdentityDB{}
	}

	opts := &lsOptions{
		longFormat: true,
		showOwner:  true,
		showGroup:  true,
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
	original := lsIdentityDBLoader
	t.Cleanup(func() {
		lsIdentityDBLoader = original
	})

	calls := 0
	lsIdentityDBLoader = func(context.Context, *Invocation) *permissionIdentityDB {
		calls++
		return &permissionIdentityDB{}
	}

	opts := &lsOptions{
		longFormat: true,
		showOwner:  true,
		showGroup:  true,
		numericIDs: true,
	}

	primeLSIdentityDB(context.Background(), &Invocation{}, opts)

	if calls != 0 {
		t.Fatalf("loader calls = %d, want 0", calls)
	}
	if opts.identityDB != nil {
		t.Fatalf("identityDB = %#v, want nil", opts.identityDB)
	}
}
