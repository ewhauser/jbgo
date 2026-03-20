package interp

import (
	"bytes"
	"testing"
)

type trackingCloser struct {
	closes int
}

func (c *trackingCloser) Close() error {
	c.closes++
	return nil
}

func TestSetFDClosesOwnedDescriptorWhenRemoved(t *testing.T) {
	t.Parallel()

	closer := &trackingCloser{}
	r := &Runner{
		fds: map[int]*shellFD{
			5: {closer: closer, owned: true},
		},
	}

	r.setFD(5, nil)

	if got := closer.closes; got != 1 {
		t.Fatalf("closes = %d, want 1", got)
	}
}

func TestSetFDPreservesSharedDescriptorUntilLastReference(t *testing.T) {
	t.Parallel()

	closer := &trackingCloser{}
	shared := &shellFD{closer: closer, owned: true}
	r := &Runner{
		fds: map[int]*shellFD{
			5: shared,
			6: shared,
		},
	}

	r.setFD(5, nil)
	if got := closer.closes; got != 0 {
		t.Fatalf("closes after first delete = %d, want 0", got)
	}

	r.setFD(6, nil)
	if got := closer.closes; got != 1 {
		t.Fatalf("closes after second delete = %d, want 1", got)
	}
}

func TestSetFDDoesNotCloseNonOwnedStandardDescriptors(t *testing.T) {
	t.Parallel()

	closer := &trackingCloser{}
	r := &Runner{
		stdout: &bytes.Buffer{},
		fds: map[int]*shellFD{
			1: {writer: &bytes.Buffer{}, closer: closer},
		},
	}

	r.setFD(1, newShellOutputFD(&bytes.Buffer{}))

	if got := closer.closes; got != 0 {
		t.Fatalf("closes = %d, want 0", got)
	}
}
