//go:build linux

package probe

import (
	"os/exec"
	"syscall"
)

func prepareProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}

// The systemd unit supplies hard CPU/memory limits for the probe and its child.
func constrainProcess(_ *exec.Cmd) (func(), error) { return func() {}, nil }
