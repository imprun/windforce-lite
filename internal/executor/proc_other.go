//go:build !unix

package executor

import "os/exec"

func setProcAttr(cmd *exec.Cmd) {}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
