//go:build !windows && !linux

package main

import (
	"errors"
)

var errServiceUnsupported = errors.New("фоновая служба поддерживается только на Windows и Linux с systemd")

func serviceSupported() bool { return false }

func servicePrivileged() bool { return false }

func serviceDir() string { return "" }

func serviceTroubleshootingHint() string { return errServiceUnsupported.Error() }

func currentServiceStatus() (serviceStatus, error) { return serviceStatus{}, errServiceUnsupported }

func serviceRunning() bool { return false }

func serviceInstalled() bool { return false }

func serviceInstall(_ string) error { return errServiceUnsupported }

func serviceRemove() error { return errServiceUnsupported }

func serviceSync(_ string) error { return errServiceUnsupported }

func serviceStart() error { return errServiceUnsupported }

func serviceStop() error { return errServiceUnsupported }

func serviceSetAutostart(bool) error { return errServiceUnsupported }

func serviceElevate(_ string, _ string) error { return errServiceUnsupported }

func samePath(a, b string) bool { return a == b }
