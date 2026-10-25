//go:build !windows

package main

import (
	"bufio"
	"errors"
)

func windowsServiceSupported() bool { return false }

func windowsServiceRunning() bool { return false }

func windowsServiceInstalled() bool { return false }

func windowsServicePrompt(_ *bufio.Reader, _ string) (bool, error) { return false, nil }

func installWindowsService(_ string) error {
	return errors.New("установка службы поддерживается только на Windows")
}

func uninstallWindowsService() error {
	return errors.New("удаление службы поддерживается только на Windows")
}
