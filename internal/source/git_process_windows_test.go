//go:build windows

package source

import (
	"os/exec"
	"testing"
)

func TestHideGitCommandWindowSetsWindowsSysProcAttr(t *testing.T) {
	cmd := exec.Command("git", "--version")

	hideGitCommandWindow(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr was not set")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("HideWindow was not enabled")
	}
}
