//go:build unix

package engine

import (
	"os/exec"
	"syscall"
	"time"
)

// prepareCmd runs the step in its own process group, so a timeout kills
// the whole tree, not only the shell.
func prepareCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
}
