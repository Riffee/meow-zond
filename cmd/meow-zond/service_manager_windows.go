//go:build windows

package main

import (
	"errors"
	"fmt"
	"meowzond/internal/probe"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const probeServiceName = "MeowStatusProbe"
const probeServiceDisplay = "Meow Status Probe"

func serviceSupported() bool { return true }

func servicePrivileged() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

func serviceTroubleshootingHint() string {
	return `проверьте «Службы» (services.msc) и C:\ProgramData\MeowProbe\probe-service.log`
}

func serviceDir() string {
	root := os.Getenv("ProgramData")
	if root == "" {
		root = `C:\ProgramData`
	}
	return filepath.Join(root, "MeowProbe")
}

func serviceHandle() (*mgr.Mgr, *mgr.Service, error) {
	m, err := mgr.Connect()
	if err != nil {
		return nil, nil, err
	}
	s, err := m.OpenService(probeServiceName)
	if err != nil {
		m.Disconnect()
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return nil, nil, os.ErrNotExist
		}
		return nil, nil, err
	}
	return m, s, nil
}

// queryService asks the service control manager for the installed state using
// read-only access. mgr.Connect requests full access and would make a normal
// console launch fail with access denied before the user can even see the menu.
func queryService() (bool, svc.State, error) {
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return false, 0, err
	}
	defer windows.CloseServiceHandle(manager)
	name, err := windows.UTF16PtrFromString(probeServiceName)
	if err != nil {
		return false, 0, err
	}
	service, err := windows.OpenService(manager, name, windows.SERVICE_QUERY_STATUS|windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return false, svc.Stopped, nil
		}
		return false, 0, err
	}
	defer windows.CloseServiceHandle(service)
	var status windows.SERVICE_STATUS
	if err = windows.QueryServiceStatus(service, &status); err != nil {
		return true, 0, err
	}
	return true, svc.State(status.CurrentState), nil
}

// serviceStartType reads the boot behaviour. A standard user can normally query the
// configuration; when even that is refused the installer's own choice (automatic) is
// reported instead of failing the whole status screen.
func serviceStartType() (uint32, bool) {
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return windows.SERVICE_AUTO_START, false
	}
	defer windows.CloseServiceHandle(manager)
	name, err := windows.UTF16PtrFromString(probeServiceName)
	if err != nil {
		return windows.SERVICE_AUTO_START, false
	}
	service, err := windows.OpenService(manager, name, windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		return windows.SERVICE_AUTO_START, false
	}
	defer windows.CloseServiceHandle(service)
	needed := uint32(0)
	if err := windows.QueryServiceConfig(service, nil, 0, &needed); err != nil && needed == 0 {
		return windows.SERVICE_AUTO_START, false
	}
	buffer := make([]byte, needed)
	config := (*windows.QUERY_SERVICE_CONFIG)(unsafe.Pointer(&buffer[0]))
	if err := windows.QueryServiceConfig(service, config, uint32(len(buffer)), &needed); err != nil {
		return windows.SERVICE_AUTO_START, false
	}
	return config.StartType, true
}

func currentServiceStatus() (serviceStatus, error) {
	var st serviceStatus
	installed, state, err := queryService()
	st.installed = installed
	if err != nil {
		return st, err
	}
	if !installed {
		return st, nil
	}
	st.running = state == svc.Running
	startType, _ := serviceStartType()
	st.autostart = startType == windows.SERVICE_AUTO_START || startType == windows.SERVICE_BOOT_START
	st.state = windowsServiceStateText(state)
	return st, nil
}

func windowsServiceStateText(state svc.State) string {
	switch state {
	case svc.Running:
		return "работает"
	case svc.StartPending:
		return "запускается"
	case svc.StopPending:
		return "останавливается"
	default:
		return "остановлена"
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
	if !servicePrivileged() {
		return errors.New("нужны права администратора: запустите программу от имени администратора")
	}
	if installed, _, err := queryService(); err != nil {
		return err
	} else if installed {
		return fmt.Errorf("служба %s уже установлена", probeServiceName)
	}
	c, err := registeredConfig(configPath)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir := serviceDir()
	if err = os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("не удалось создать %s: %w", dir, err)
	}
	serviceExe := filepath.Join(dir, "meow-probe.exe")
	if !samePath(exe, serviceExe) {
		if err = copyFile(exe, serviceExe); err != nil {
			return fmt.Errorf("не удалось скопировать агент: %w", err)
		}
	}
	if err = prepareServiceConfig(&c, dir); err != nil {
		return err
	}
	serviceConfig := serviceConfigPath(dir)
	if err = probe.Save(serviceConfig, c); err != nil {
		return fmt.Errorf("не удалось сохранить настройки службы: %w", err)
	}
	if err = grantServiceAccess(dir, serviceExe, serviceConfig); err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("не удалось подключиться к менеджеру служб: %w", err)
	}
	defer m.Disconnect()
	s, err := m.CreateService(probeServiceName, serviceExe, mgr.Config{
		DisplayName:      probeServiceDisplay,
		Description:      "Фоновый сетевой зонд meow! status",
		StartType:        mgr.StartAutomatic,
		ServiceStartName: `NT AUTHORITY\LocalService`,
		ErrorControl:     windows.SERVICE_ERROR_NORMAL,
		Dependencies:     []string{"Tcpip"},
	}, "-headless", "-config", serviceConfig)
	if err != nil {
		return fmt.Errorf("не удалось создать службу: %w", err)
	}
	defer s.Close()
	actions := []mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: time.Minute}, {Type: mgr.ServiceRestart, Delay: time.Minute}}
	_ = s.SetRecoveryActions(actions, 86400)
	_ = s.SetRecoveryActionsOnNonCrashFailures(true)
	if err = s.Start(); err != nil {
		return fmt.Errorf("служба создана, но не запустилась: %w", err)
	}
	return nil
}

// grantServiceAccess gives the LocalService account read access to the files the
// service needs; without it the service starts and immediately fails on its settings.
func grantServiceAccess(dir string, files ...string) error {
	if out, err := exec.Command("icacls.exe", dir, "/grant", "*S-1-5-19:(OI)(CI)M", "/T").CombinedOutput(); err != nil {
		return fmt.Errorf("не удалось выдать права LocalService: %v: %s", err, strings.TrimSpace(string(out)))
	}
	for _, path := range files {
		if out, err := exec.Command("icacls.exe", path, "/grant", "*S-1-5-19:RX").CombinedOutput(); err != nil {
			return fmt.Errorf("не удалось выдать доступ к %s: %v: %s", path, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// serviceSync replaces the service settings with the ones entered in this console —
// including a new status-page address — and refreshes the program file. The service
// process must be stopped first: Windows does not let a running executable be replaced.
func serviceSync(consoleConfig string) error {
	if !servicePrivileged() {
		return errors.New("нужны права администратора")
	}
	installed, _, err := queryService()
	if err != nil {
		return err
	}
	if !installed {
		return errors.New("фоновая служба не установлена")
	}
	c, err := registeredConfig(consoleConfig)
	if err != nil {
		return err
	}
	if serviceRunning() {
		if err = serviceStop(); err != nil {
			return err
		}
	}
	dir := serviceDir()
	if err = os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("не удалось создать %s: %w", dir, err)
	}
	serviceExe := filepath.Join(dir, "meow-probe.exe")
	if exe, e := os.Executable(); e == nil {
		if !samePath(exe, serviceExe) {
			if e = copyFile(exe, serviceExe); e != nil {
				return fmt.Errorf("не удалось обновить агент: %w", e)
			}
		}
	}
	if err = prepareServiceConfig(&c, dir); err != nil {
		return err
	}
	serviceConfig := serviceConfigPath(dir)
	if err = probe.Save(serviceConfig, c); err != nil {
		return fmt.Errorf("не удалось сохранить настройки службы: %w", err)
	}
	if err = grantServiceAccess(dir, serviceExe, serviceConfig); err != nil {
		return err
	}
	return serviceStart()
}

func serviceStart() error {
	if !servicePrivileged() {
		return errors.New("нужны права администратора")
	}
	m, s, err := serviceHandle()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	if status, e := s.Query(); e == nil && status.State == svc.Running {
		return nil
	}
	if err = s.Start(); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, e := s.Query()
		if e == nil && status.State == svc.Running {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("служба не перешла в состояние RUNNING")
}

func serviceStop() error {
	if !servicePrivileged() {
		return errors.New("нужны права администратора")
	}
	m, s, err := serviceHandle()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	status, err := s.Query()
	if err != nil {
		return err
	}
	if status.State == svc.Stopped {
		return nil
	}
	if _, err = s.Control(svc.Stop); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		status, e := s.Query()
		if e == nil && status.State == svc.Stopped {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("служба не остановилась за 15 секунд")
}

// serviceSetAutostart keeps the service installed and only changes whether Windows
// starts it on boot: stopping autostart is what the user asks for when the probe
// must not come back after a reboot, not when the program should disappear.
func serviceSetAutostart(on bool) error {
	if !servicePrivileged() {
		return errors.New("нужны права администратора")
	}
	m, s, err := serviceHandle()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	// UpdateConfig writes every field it is given, so the current configuration is
	// read first and only the start type is changed.
	config, err := s.Config()
	if err != nil {
		return fmt.Errorf("не удалось прочитать настройки службы: %w", err)
	}
	config.StartType = windows.SERVICE_DEMAND_START
	if on {
		config.StartType = windows.SERVICE_AUTO_START
	}
	if err = s.UpdateConfig(config); err != nil {
		return fmt.Errorf("не удалось изменить автозапуск: %w", err)
	}
	if on {
		return serviceStart()
	}
	return nil
}

func serviceRemove() error {
	if !servicePrivileged() {
		return errors.New("нужны права администратора")
	}
	if serviceRunning() {
		if err := serviceStop(); err != nil {
			return fmt.Errorf("служба осталась запущенной и не удалена: %w", err)
		}
	}
	m, s, err := serviceHandle()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	return s.Delete()
}

// serviceElevate runs one operation in an elevated helper process through UAC and
// maps the cancelled dialog to a plain answer.
func serviceElevate(action, configPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return err
	}
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	args, err := windows.UTF16PtrFromString("-" + action + " -config " + syscall.EscapeArg(abs))
	if err != nil {
		return err
	}
	if err = windows.ShellExecute(0, verb, file, args, nil, windows.SW_SHOWNORMAL); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return errors.New("подтверждение прав не получено")
		}
		return fmt.Errorf("запуск от имени администратора не удался: %w", err)
	}
	return nil
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return false
	}
	return strings.EqualFold(filepath.Clean(aa), filepath.Clean(bb))
}
