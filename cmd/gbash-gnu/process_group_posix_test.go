//go:build !windows

package main

import (
	"os/exec"
	"testing"
)

func TestConfigureIsolatedProcessGroupSetsSetpgid(t *testing.T) {
	cmd := exec.Command("sh", "-c", "true")

	configureIsolatedProcessGroup(cmd)

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatalf("configureIsolatedProcessGroup() did not enable Setpgid")
	}
}
