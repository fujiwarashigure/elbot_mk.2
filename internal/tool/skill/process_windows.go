//go:build windows

package skill

import (
	"fmt"
	"os/exec"
)

func configureCommandProcess(cmd *exec.Cmd) {}

func killCommandProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", fmt.Sprint(cmd.Process.Pid)).Run()
	_ = cmd.Process.Kill()
}
