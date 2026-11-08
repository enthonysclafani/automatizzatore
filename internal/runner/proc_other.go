//go:build !unix

package runner

import (
	"os/exec"
	"time"
)

func prepareCmd(cmd *exec.Cmd) {
	cmd.WaitDelay = 5 * time.Second
}
