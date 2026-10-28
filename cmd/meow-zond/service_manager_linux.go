//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"meowzond/internal/probe"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The Linux background service is a systemd unit. Names are fixed so the .deb can
// ship the same unit the program generates; they are variables only so the tests can
// point them at a temporary root instead of the real /etc.
const serviceName = "meow-zond.service"

var (
	serviceEtcDir     = "/etc/systemd/system"
	servicePackageDir = "/lib/systemd/system"
	serviceBinaryPath = "/usr/local/bin/meow-zond"
	serviceStateDir   = "/var/lib/meow-zond"
	serviceRuntimeDir = "/run/systemd/system"
)

// systemBinaryDirs are the locations where a probe installed by a package manager
// already lives and therefore needs no copy.
var systemBinaryDirs = []string{"/usr/local/bin", "/usr/bin", "/bin", "/sbin", "/usr/sbin"}

func serviceSupported() bool {
	return systemdMissing() == nil
}

// systemdMissing keeps the reason in Russian instead of leaking the exec error of
// a binary that is not installed: systemd exposes /run/systemd/system only when it
// is PID 1, so sysvinit, containers and WSL without systemd have nothing to talk to.
func systemdMissing() error {
	if _, e := os.Stat(serviceRuntimeDir); e != nil {
		return errors.New("systemd не найден: фоновая служба доступна только на системах с systemd")
	}
	if _, e := exec.LookPath("systemctl"); e != nil {
		return errors.New("не найден systemctl: установите systemd")
	}
	return nil
}

func servicePrivileged() bool { return os.Geteuid() == 0 }

func serviceDir() string { return serviceStateDir }

func serviceTroubleshootingHint() string {
	return "проверьте sudo systemctl status " + serviceName + " и sudo journalctl -u " + serviceName
}

func unitFile() string { return filepath.Join(serviceEtcDir, serviceName) }

func packagedUnit() string { return filepath.Join(servicePackageDir, serviceName) }

func installedUnitPath() (string, bool) {
	// A generated unit in /etc overrides the packaged one; the package file is
	// owned by dpkg and is only read here.
	for _, path := range []string{unitFile(), packagedUnit()} {
		if info, e := os.Stat(path); e == nil && info.Mode().IsRegular() {
			return path, true
		}
	}
	return "", false
}

func systemctl(ctx context.Context, args ...string) (string, error) {
	if e := systemdMissing(); e != nil {
		return "", e
	}
	out, e := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if e != nil {
		if text != "" {
			return text, fmt.Errorf("systemctl %s: %s", strings.Join(args, " "), text)
		}
		return text, fmt.Errorf("systemctl %s: %w", strings.Join(args, " "), e)
	}
	return text, nil
}

// systemctlFlag reads a yes/no answer that systemd reports through the exit code:
// 0 is yes, 1 and 3 are honest no-answers, anything else is a real failure.
func systemctlFlag(state string) (bool, string, error) {
	if e := systemdMissing(); e != nil {
		return false, "", e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, e := exec.CommandContext(ctx, "systemctl", state, serviceName).CombinedOutput()
	answer := strings.TrimSpace(string(out))
	if e == nil {
		return true, answer, nil
	}
	var exit *exec.ExitError
	if errors.As(e, &exit) {
		switch exit.ExitCode() {
		case 0:
			return true, answer, nil
		case 1, 3:
			return false, answer, nil
		}
	}
	if answer == "" {
		answer = e.Error()
	}
	return false, answer, fmt.Errorf("systemctl %s: %s", state, answer)
}

func currentServiceStatus() (serviceStatus, error) {
	var st serviceStatus
	if _, st.installed = installedUnitPath(); !st.installed {
		return st, nil
	}
	active, answer, e := systemctlFlag("is-active")
	if e != nil {
		return st, e
	}
	enabled, _, e := systemctlFlag("is-enabled")
	if e != nil {
		return st, e
	}
	st.running, st.autostart = active, enabled
	st.state = systemdStateText(answer)
	return st, nil
}

func systemdStateText(answer string) string {
	switch answer {
	case "active":
		return "работает"
	case "activating":
		return "запускается"
	case "deactivating":
		return "останавливается"
	case "inactive":
		return "остановлена"
	case "failed":
		return "аварийно завершилась"
	default:
		return answer
	}
}

func serviceRunning() bool {
	st, e := currentServiceStatus()
	return e == nil && st.running
}

func serviceInstalled() bool {
	st, e := currentServiceStatus()
	return e == nil && st.installed
}

func serviceInstall(configPath string) error {
	if e := systemdMissing(); e != nil {
		return e
	}
	if !servicePrivileged() {
		return errors.New("нужны права root: выполните sudo meow-zond -install-service")
	}
	c, e := registeredConfig(configPath)
	if e != nil {
		return e
	}
	unitPath, present := installedUnitPath()
	if present {
		st, e := currentServiceStatus()
		if e != nil {
			return e
		}
		if st.running || st.autostart {
			return fmt.Errorf("служба %s уже установлена", serviceName)
		}
	}
	exe, e := serviceBinary()
	if e != nil {
		return e
	}
	if !present || filepath.Clean(exe) != filepath.Clean(packagedExecPath(unitPath)) {
		// A unit that came from the .deb is only rewritten when it points at another
		// binary: dpkg owns that file, and enabling it is the whole installation.
		if e = writeUnit(exe, serviceConfigPath(serviceDir())); e != nil {
			return e
		}
	}
	if e = prepareServiceState(&c); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, e = systemctl(ctx, "daemon-reload"); e != nil {
		return e
	}
	if _, e = systemctl(ctx, "enable", "--now", serviceName); e != nil {
		return e
	}
	return nil
}

// packagedExecPath reads ExecStart out of a unit file so the program can tell whether
// the packaged unit already points at the running binary.
func packagedExecPath(unitPath string) string {
	body, e := os.ReadFile(unitPath)
	if e != nil {
		return serviceBinaryPath
	}
	for _, line := range strings.Split(string(body), "\n") {
		name, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found && name == "ExecStart" {
			fields := strings.Fields(value)
			if len(fields) > 0 {
				return fields[0]
			}
		}
	}
	return serviceBinaryPath
}

// serviceBinary returns the path systemd must exec. A binary that already lives in
// a system directory is used as is; a downloaded one is copied, because the service
// must not depend on a file the user can move or delete.
func serviceBinary() (string, error) {
	exe, e := os.Executable()
	if e != nil {
		return "", e
	}
	exe = filepath.Clean(exe)
	for _, dir := range systemBinaryDirs {
		if filepath.Dir(exe) == dir {
			return exe, nil
		}
	}
	if e = os.MkdirAll(filepath.Dir(serviceBinaryPath), 0755); e != nil {
		return "", fmt.Errorf("не удалось создать %s: %w", filepath.Dir(serviceBinaryPath), e)
	}
	if e = copyFile(exe, serviceBinaryPath); e != nil {
		return "", fmt.Errorf("не удалось установить программу: %w", e)
	}
	return serviceBinaryPath, nil
}

// prepareServiceState moves the probe to service-owned paths and saves its settings
// where only root can read them.
func prepareServiceState(c *probe.Config) error {
	dir := serviceDir()
	if e := os.MkdirAll(dir, 0700); e != nil {
		return fmt.Errorf("не удалось создать %s: %w", dir, e)
	}
	if e := prepareServiceConfig(c, dir); e != nil {
		return e
	}
	path := serviceConfigPath(dir)
	if e := probe.Save(path, *c); e != nil {
		return fmt.Errorf("не удалось сохранить настройки службы: %w", e)
	}
	return os.Chmod(path, 0600)
}

func writeUnit(exe, config string) error {
	unit := fmt.Sprintf(`[Unit]
Description=meow! status — сетевой зонд
Documentation=https://github.com/Riffee/meow-zond
After=network-online.target
Wants=network-online.target
# Stay inactive until the probe is connected: the packaged unit ships disabled
# and has no settings file yet, so a start would only restart-loop.
ConditionPathExists=%s

[Service]
Type=simple
ExecStart=%s -headless -config %s
Restart=always
RestartSec=5
TimeoutStopSec=20
StateDirectory=meow-zond
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
NoNewPrivileges=yes
CapabilityBoundingSet=CAP_NET_RAW
AmbientCapabilities=CAP_NET_RAW
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK

[Install]
WantedBy=multi-user.target
`, config, exe, config)
	if e := os.MkdirAll(serviceEtcDir, 0755); e != nil {
		return fmt.Errorf("не удалось создать %s: %w", serviceEtcDir, e)
	}
	tmp := unitFile() + ".new"
	if e := os.WriteFile(tmp, []byte(unit), 0644); e != nil {
		return fmt.Errorf("не удалось записать службу: %w", e)
	}
	if e := os.Rename(tmp, unitFile()); e != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("не удалось записать службу: %w", e)
	}
	return nil
}

func serviceStart() error {
	if !servicePrivileged() {
		return errors.New("нужны права root: выполните sudo meow-zond -start-service")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, e := systemctl(ctx, "start", serviceName)
	return e
}

func serviceStop() error {
	if !servicePrivileged() {
		return errors.New("нужны права root: выполните sudo meow-zond -stop-service")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, e := systemctl(ctx, "stop", serviceName)
	return e
}

func serviceSetAutostart(on bool) error {
	if !servicePrivileged() {
		return errors.New("нужны права root: выполните это действие через sudo")
	}
	flag := "disable"
	if on {
		flag = "enable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, installed := installedUnitPath(); !installed {
		return fmt.Errorf("служба %s не установлена", serviceName)
	}
	if _, e := systemctl(ctx, flag, serviceName); e != nil {
		return e
	}
	if on {
		return serviceStart()
	}
	return nil
}

func serviceSync(consoleConfig string) error {
	if !servicePrivileged() {
		return errors.New("нужны права root: выполните это действие через sudo")
	}
	if _, installed := installedUnitPath(); !installed {
		return errors.New("фоновая служба не установлена")
	}
	c, e := registeredConfig(consoleConfig)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if st, e := currentServiceStatus(); e == nil && st.running {
		if _, e = systemctl(ctx, "stop", serviceName); e != nil {
			return e
		}
	}
	exe, e := serviceBinary()
	if e != nil {
		return e
	}
	config := serviceConfigPath(serviceDir())
	if e = prepareServiceState(&c); e != nil {
		return e
	}
	if e = writeUnit(exe, config); e != nil {
		return e
	}
	if _, e = systemctl(ctx, "daemon-reload"); e != nil {
		return e
	}
	if _, e = systemctl(ctx, "enable", "--now", serviceName); e != nil {
		return e
	}
	return nil
}

// serviceRemove stops reporting and takes the unit out of the boot sequence. A unit
// that came from the .deb stays on disk: dpkg owns that file and removes it with the
// package.
func serviceRemove() error {
	if !servicePrivileged() {
		return errors.New("нужны права root: выполните это действие через sudo")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, e := systemctl(ctx, "disable", "--now", serviceName); e != nil && !strings.Contains(e.Error(), "No such file") {
		return e
	}
	if e := os.Remove(unitFile()); e != nil && !errors.Is(e, os.ErrNotExist) {
		return fmt.Errorf("не удалось удалить %s: %w", unitFile(), e)
	}
	if _, e := systemctl(ctx, "daemon-reload"); e != nil {
		return e
	}
	return nil
}

func serviceElevate(action, configPath string) error {
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	abs, e := filepath.Abs(configPath)
	if e != nil {
		return e
	}
	args := []string{"-" + action, "-config", abs}
	if path, lookErr := exec.LookPath("sudo"); lookErr == nil {
		return runElevated(append([]string{path, "-E"}, args...))
	}
	if path, lookErr := exec.LookPath("pkexec"); lookErr == nil {
		return runElevated(append([]string{path, exe}, args...))
	}
	return fmt.Errorf("нужны права root: выполните sudo %s", strings.Join(append([]string{filepath.Base(exe)}, args...), " "))
}

// runElevated keeps the console attached to the child so sudo can ask for the
// password and print its own diagnosis.
func runElevated(args []string) error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if e := cmd.Run(); e != nil {
		return fmt.Errorf("действие с правами root не выполнено (%w)", e)
	}
	return nil
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return false
	}
	return filepath.Clean(aa) == filepath.Clean(bb)
}
