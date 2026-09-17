//go:build unix

package whatif

import (
	"os/exec"
	"syscall"
)

// isolate puts the script in its own process group.
//
// Without it, killing a script that has hung kills the script but not what it
// spawned: a `sleep` or a compressor keeps running, and keeps the inherited
// stderr open, so the tool appears to hang for as long as the orphan lives
// even though it gave up on the intervention.
func isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminate kills the script and everything it started.
func terminate(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
