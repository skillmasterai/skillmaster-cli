//go:build !unix

package main

import "os/exec"

// configureProcessGroup is the portable fallback.
//
// **No process-group control here.** The default `exec.CommandContext` cancellation kills the
// process it started and nothing else; on platforms without POSIX process groups a launcher's own
// children may outlive the timeout. That is the best portable behaviour, and it still terminates
// the process this run is waiting on, so the exit code is 124 as promised.
func configureProcessGroup(cmd *exec.Cmd) {}
