//go:build windows

package media

import (
	"fmt"
	"os/exec"
)

func configureWorkerProcess(cmd *exec.Cmd) {}

func killWorkerProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", fmt.Sprint(cmd.Process.Pid)).Run()
	_ = cmd.Process.Kill()
}
