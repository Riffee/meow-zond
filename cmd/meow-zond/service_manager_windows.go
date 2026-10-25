//go:build windows

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"meowzond/internal/probe"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const probeServiceName = "MeowStatusProbe"
const probeServiceDisplay = "Meow Status Probe"

func windowsServiceSupported() bool { return true }

func elevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
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

func windowsServiceRunning() bool {
	installed, state, err := serviceState()
	return err == nil && installed && state == svc.Running
}

func windowsServiceInstalled() bool {
	installed, _, err := serviceState()
	return err == nil && installed
}

func serviceState() (bool, svc.State, error) {
	// Read-only access works for standard users. mgr.Connect requests
	// SC_MANAGER_ALL_ACCESS and would make a normal console launch fail with
	// access denied before the user can even see the menu.
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return false, 0, err
	}
	defer windows.CloseServiceHandle(manager)
	name, err := windows.UTF16PtrFromString(probeServiceName)
	if err != nil {
		return false, 0, err
	}
	service, err := windows.OpenService(manager, name, windows.SERVICE_QUERY_STATUS)
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

func windowsServicePrompt(input *bufio.Reader, configPath string) (bool, error) {
	installed, state, err := serviceState()
	if err != nil {
		fmt.Println()
		fmt.Println("Не удалось проверить фоновую службу Windows. Продолжаю в консоли.")
		fmt.Println("Причина:", err)
		return false, nil
	}
	if installed {
		running := state == svc.Running
		fmt.Println()
		if running {
			fmt.Printf("Зонд уже работает в фоне как служба Windows «%s».\n", probeServiceDisplay)
		} else {
			fmt.Printf("Служба Windows «%s» установлена, но остановлена.\n", probeServiceDisplay)
		}
		fmt.Println("1 — " + map[bool]string{true: "остановить службу", false: "запустить службу"}[running])
		fmt.Println("2 — удалить службу")
		fmt.Println("0 — выход")
		for {
			fmt.Print("Выбор: ")
			line, e := readLine(input)
			if e != nil {
				return true, nil
			}
			switch line {
			case "1":
				if running {
					err = stopWindowsService()
				} else {
					err = startWindowsService()
				}
				if err != nil {
					fmt.Println("Ошибка:", err)
				} else if running {
					fmt.Println("Служба остановлена.")
				} else {
					fmt.Println("Служба запущена.")
				}
				return true, nil
			case "2":
				if !elevated() {
					fmt.Println("Для удаления службы запустите программу от имени администратора.")
					continue
				}
				if err = uninstallWindowsService(); err != nil {
					fmt.Println("Ошибка:", err)
					continue
				}
				fmt.Println("Служба удалена.")
				return true, nil
			case "0", "q":
				return true, nil
			default:
				fmt.Println("Введите 1, 2 или 0.")
			}
		}
	}
	if elevated() {
		fmt.Println()
		fmt.Println("Установить фоновую службу Windows?")
		fmt.Println("Зонд продолжит работать после закрытия этого окна и после перезагрузки.")
		fmt.Println("1 — установить и запустить")
		fmt.Println("2 — остаться в консоли")
		for {
			fmt.Print("Выбор: ")
			line, e := readLine(input)
			if e != nil {
				return false, nil
			}
			switch line {
			case "1":
				if err := installWindowsService(configPath); err != nil {
					return false, fmt.Errorf("не удалось установить службу: %w", err)
				}
				fmt.Println()
				fmt.Println("Готово. Служба установлена и запущена — это окно можно закрыть.")
				return true, nil
			case "2":
				return false, nil
			case "":
			default:
				fmt.Println("Введите 1 или 2.")
			}
		}
	}
	fmt.Println()
	fmt.Println("Для работы без открытого окна нужна фоновая служба Windows.")
	fmt.Println("Windows покажет запрос на разрешение администратора.")
	fmt.Println("1 — установить и запустить службу")
	fmt.Println("2 — остаться в консоли")
	for {
		fmt.Print("Выбор: ")
		line, e := readLine(input)
		if e != nil {
			return false, nil
		}
		switch line {
		case "1":
			if err := elevateInstall(configPath); err != nil {
				fmt.Println("Не удалось запросить права администратора:", err)
				continue
			}
			fmt.Println("Подтвердите установку в окне Windows. Эта консоль больше не нужна.")
			return true, nil
		case "2":
			return false, nil
		case "":
		default:
			fmt.Println("Введите 1 или 2.")
		}
	}
}

func elevateInstall(configPath string) error {
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
	args, err := windows.UTF16PtrFromString("-install-service -config " + syscall.EscapeArg(abs))
	if err != nil {
		return err
	}
	if err = windows.ShellExecute(0, verb, file, args, nil, windows.SW_SHOWNORMAL); err != nil {
		return err
	}
	return nil
}

func installWindowsService(configPath string) error {
	if !elevated() {
		return errors.New("нужны права администратора: запустите программу от имени администратора")
	}
	if installed, _, err := serviceState(); err != nil {
		return err
	} else if installed {
		return fmt.Errorf("служба %s уже установлена", probeServiceName)
	}
	c, err := probe.Load(configPath)
	if err != nil {
		return fmt.Errorf("не удалось прочитать настройки: %w", err)
	}
	if c.ProbeID == "" || len(c.Token) < 32 {
		return errors.New("сначала подключите зонд")
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
	if c.XrayPath != "" {
		if info, statErr := os.Stat(c.XrayPath); statErr == nil && info.Mode().IsRegular() {
			xrayDest := filepath.Join(dir, filepath.Base(c.XrayPath))
			if !samePath(c.XrayPath, xrayDest) {
				if err = copyFile(c.XrayPath, xrayDest); err != nil {
					return fmt.Errorf("не удалось скопировать Xray: %w", err)
				}
			}
			c.XrayPath = xrayDest
		}
	}
	c.Spool = filepath.Join(dir, "probe-results.json")
	serviceConfig := filepath.Join(dir, "probe.json")
	if err = probe.Save(serviceConfig, c); err != nil {
		return fmt.Errorf("не удалось сохранить настройки службы: %w", err)
	}
	if out, err := exec.Command("icacls.exe", dir, "/grant", "*S-1-5-19:(OI)(CI)M", "/T").CombinedOutput(); err != nil {
		return fmt.Errorf("не удалось выдать права LocalService: %v: %s", err, strings.TrimSpace(string(out)))
	}
	for _, path := range []string{serviceExe, serviceConfig} {
		if out, err := exec.Command("icacls.exe", path, "/grant", "*S-1-5-19:RX").CombinedOutput(); err != nil {
			return fmt.Errorf("не удалось выдать доступ к %s: %v: %s", path, err, strings.TrimSpace(string(out)))
		}
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
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, e := s.Query()
		if e == nil && status.State == svc.Running {
			return nil
		}
		if e == nil && status.State == svc.Stopped {
			return errors.New("служба завершилась сразу после запуска; проверьте probe-service.log")
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("служба запускается слишком долго; проверьте её состояние в services.msc")
}

func startWindowsService() error {
	if !elevated() {
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

func stopWindowsService() error {
	if !elevated() {
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

func uninstallWindowsService() error {
	if !elevated() {
		return errors.New("нужны права администратора")
	}
	m, s, err := serviceHandle()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer m.Disconnect()
	_ = stopWindowsService()
	defer s.Close()
	if err = s.Delete(); err != nil {
		return err
	}
	return nil
}

func serviceDir() string {
	root := os.Getenv("ProgramData")
	if root == "" {
		root = `C:\ProgramData`
	}
	return filepath.Join(root, "MeowProbe")
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	_ = os.Remove(dst)
	return os.Rename(tmp, dst)
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return false
	}
	return strings.EqualFold(filepath.Clean(aa), filepath.Clean(bb))
}
