//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// configureProcessGroup makes the child its own process group and makes cancellation kill the
// whole group.
//
// **A timeout has to take the grandchildren too.** A launcher that starts an interpreter, or a
// shell running a pipeline, leaves its children running if only the leader is killed; they would
// keep writing after the run has answered 124. `Setpgid` at start and a negative pid at cancel is
// the POSIX way to say "everything this run started" (ADR 0222 后果).
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
