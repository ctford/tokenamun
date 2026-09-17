//go:build !unix

package whatif

import "os/exec"

// isolate is a no-op where process groups work differently. A hung
// intervention is still abandoned; only its children may outlive it.
func isolate(*exec.Cmd) {}

func terminate(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
