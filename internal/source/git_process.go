//go:build !windows

package source

import "os/exec"

func hideGitCommandWindow(cmd *exec.Cmd) {
}
