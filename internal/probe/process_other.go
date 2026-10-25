//go:build !windows && !linux

package probe

import "os/exec"

func prepareProcess(_ *exec.Cmd)                   {}
func constrainProcess(_ *exec.Cmd) (func(), error) { return func() {}, nil }
