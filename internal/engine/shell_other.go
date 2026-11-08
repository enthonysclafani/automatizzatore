//go:build !unix

package engine

import (
	"os/exec"
	"time"
)

func prepareCmd(cmd *exec.Cmd) {
	cmd.WaitDelay = 5 * time.Second
}
