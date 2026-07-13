//go:build windows

package source

import (
	"os/exec"
	"syscall"
)

func hideGitCommandWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
