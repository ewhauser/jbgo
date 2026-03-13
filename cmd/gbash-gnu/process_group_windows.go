//go:build windows

package main

import "os/exec"

func configureIsolatedProcessGroup(cmd *exec.Cmd) {}

func terminateIsolatedProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
