//go:build unix

package std

import (
	"os/exec"
	"syscall"
)

// ownGroup puts the child in its own process group, so a timeout can end the
// processes it started too.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
