package main

import (
	"errors"
	"fmt"
	"io"
	"meowzond/internal/probe"
	"os"
	"path/filepath"
	"time"
)

// Background service actions. The same names are used by the interactive menu, the
// command line flags and the elevated helper process, so a platform only has to
// implement the operations it understands.
const (
	actionInstall      = "install-service"
	actionUninstall    = "uninstall-service"
	actionStart        = "start-service"
	actionStop         = "stop-service"
	actionSync         = "sync-service"
	actionEnable       = "enable-autostart"
	actionDisable      = "disable-autostart"
	actionStopDisabled = "stop-and-disable"
)

// serviceStatus is what any service manager can tell about the probe: whether the
// unit exists, whether it runs now and whether it will start on boot. The menu and
// the banner are built from these three answers only.
type serviceStatus struct {
	installed bool
	running   bool
	autostart bool
	state     string
}

// serviceActionDone explains an operation after it has been verified, not after the
// helper process was started, so the message never promises more than happened.
var serviceActionDone = map[string]string{
	actionInstall:      "Фоновая служба установлена, зонд работает и вернётся после перезагрузки.",
	actionUninstall:    "Фоновая служба остановлена и больше не запустится после перезагрузки.",
	actionStart:        "Фоновая служба запущена.",
	actionStop:         "Фоновая служба остановлена: данные не отправляются, автозапуск остаётся включённым.",
	actionSync:         "Настройки службы обновлены, зонд перезапущен.",
	actionEnable:       "Автозапуск включён: служба поднимется после перезагрузки.",
	actionDisable:      "Автозапуск выключен: служба больше не поднимется после перезагрузки.",
	actionStopDisabled: "Фоновая служба выключена и не запустится после перезагрузки.",
}

// runServiceAction performs one operation with the rights it already has.
func runServiceAction(action, configPath string) error {
	switch action {
	case actionInstall:
		return serviceInstall(configPath)
	case actionUninstall:
		return serviceRemove()
	case actionStart:
		return serviceStart()
	case actionStop:
		return serviceStop()
	case actionSync:
		return serviceSync(configPath)
	case actionEnable:
		return serviceSetAutostart(true)
	case actionDisable:
		return serviceSetAutostart(false)
	case actionStopDisabled:
		if st, e := currentServiceStatus(); e == nil && st.running {
			if e = serviceStop(); e != nil {
				return e
			}
		}
		return serviceSetAutostart(false)
	}
	return fmt.Errorf("неизвестное действие со службой: %s", action)
}

// applyServiceAction runs one operation, asking for elevation when the console is
// not privileged, and waits until the service manager reports the expected state
// instead of trusting that the elevated helper succeeded.
func applyServiceAction(action, configPath string) error {
	if servicePrivileged() {
		if e := runServiceAction(action, configPath); e != nil {
			return e
		}
	} else if e := serviceElevate(action, configPath); e != nil {
		return e
	}
	return waitForServiceState(action)
}

func waitForServiceState(action string) error {
	deadline := time.Now().Add(60 * time.Second)
	for {
		st, e := currentServiceStatus()
		if e != nil {
			return e
		}
		if serviceSettled(action, st) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("служба не перешла в нужное состояние (%s): %s", action, serviceTroubleshootingHint())
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// serviceSettled answers whether an operation reached its goal; the state we wait
// for is the same on every platform except for removal, which each platform defines.
func serviceSettled(action string, st serviceStatus) bool {
	switch action {
	case actionInstall, actionStart, actionSync, actionEnable:
		return st.installed && st.running
	case actionStop:
		return !st.running
	case actionUninstall:
		// Removal is settled once nothing runs and nothing waits for boot; on Linux
		// the packaged unit file stays on disk because dpkg owns it.
		return !st.running && !st.autostart
	case actionDisable:
		return !st.autostart
	case actionStopDisabled:
		return !st.running && !st.autostart
	}
	return true
}

func serviceStateText(st serviceStatus) string {
	if !st.installed {
		return "Фоновая служба не установлена — зонд работает, пока открыто это окно."
	}
	switch {
	case st.running && st.autostart:
		return "Фоновая служба работает и запустится после перезагрузки."
	case st.running:
		return "Фоновая служба работает, но после перезагрузки не появится."
	case st.autostart:
		return "Фоновая служба остановлена, но ждёт запуска после перезагрузки."
	default:
		return "Фоновая служба выключена: данные не отправляются, после перезагрузки она не появится."
	}
}

func serviceSummary(st serviceStatus) string {
	if !st.installed {
		return "Фоновая служба: не установлена"
	}
	switch {
	case st.running && st.autostart:
		return "Фоновая служба: работает в фоне, автозапуск включён"
	case st.running:
		return "Фоновая служба: работает в фоне, автозапуск выключен"
	case st.autostart:
		return "Фоновая служба: остановлена, автозапуск включён"
	default:
		return "Фоновая служба: выключена"
	}
}

func printServiceSummary() {
	if !serviceSupported() {
		return
	}
	fmt.Println()
	st, e := currentServiceStatus()
	if e != nil {
		fmt.Println(paint(ansiMuted, "  Фоновую службу не удалось проверить: "+e.Error()))
		return
	}
	note := serviceSummary(st)
	if st.installed && st.state != "" {
		note += " · " + st.state
	}
	color := ansiMuted
	switch {
	case st.running:
		color = ansiGreen
	case st.installed && st.autostart:
		color = ansiYellow
	}
	fmt.Println(paint(color, "  "+note))
	if st.installed {
		fmt.Println(paint(ansiMuted, "  "+serviceStateText(st)))
	}
}

// serviceMenu keeps every service operation in one place. It returns the settings
// the console should keep using: installing a service starts with the destination
// address, so the console config may have been rebound inside this menu.
func serviceMenu(p prompt, configPath string, c probe.Config) probe.Config {
	for {
		st, e := currentServiceStatus()
		fmt.Println()
		fmt.Println(paint(ansiBold, "  Фоновая служба"))
		if e != nil {
			fmt.Println("  " + paint(ansiRed, e.Error()))
			return c
		}
		fmt.Println("  " + paint(ansiMuted, serviceStateText(st)))
		fmt.Println("  " + paint(ansiMuted, "данные идут на "+c.Server))
		if st.installed {
			if st.running {
				fmt.Println("  " + paint(ansiMagenta, "1") + "  Остановить службу")
			} else {
				fmt.Println("  " + paint(ansiMagenta, "1") + "  Запустить службу")
			}
			if st.running || st.autostart {
				fmt.Println("  " + paint(ansiMagenta, "2") + "  Выключить и не запускать после перезагрузки")
			}
			if !st.autostart {
				fmt.Println("  " + paint(ansiMagenta, "3") + "  Включить автозапуск")
			}
			fmt.Println("  " + paint(ansiMagenta, "4") + "  Применить настройки этого окна и перезапустить")
			fmt.Println("  " + paint(ansiMagenta, "5") + "  Удалить службу")
		} else {
			fmt.Println("  " + paint(ansiMagenta, "1") + "  Установить и включить автозапуск")
		}
		fmt.Println("  " + paint(ansiMagenta, "0") + "  Назад")
		fmt.Print("\n  " + paint(ansiBold, "Выбор: "))
		line, e := p()
		if e != nil {
			return c
		}
		action := ""
		switch {
		case line == "0" || line == "q":
			return c
		case line == "1":
			switch {
			case !st.installed:
				action = actionInstall
			case st.running:
				action = actionStop
			default:
				action = actionStart
			}
		case line == "2" && st.installed && (st.running || st.autostart):
			action = actionStopDisabled
		case line == "3" && st.installed && !st.autostart:
			action = actionEnable
		case line == "4" && st.installed:
			action = actionSync
		case line == "5" && st.installed:
			action = actionUninstall
		default:
			hint := "1 или 0"
			if st.installed {
				hint = "1, 2, 3, 4, 5 или 0"
			}
			fmt.Println("  Введите " + hint + ".")
			continue
		}
		// Creating or refreshing a service starts from the destination address: the
		// service would keep reporting the old one otherwise.
		if action == actionInstall || action == actionSync {
			updated, e := confirmServiceTarget(p, configPath, c)
			if e != nil {
				fmt.Println("  " + paint(ansiRed, e.Error()))
				continue
			}
			c = updated
		}
		if e = applyServiceAction(action, configPath); e != nil {
			fmt.Println("  " + paint(ansiRed, "Не получилось: "+e.Error()))
			continue
		}
		fmt.Println("  " + paint(ansiGreen, serviceActionDone[action]))
		if action == actionInstall || action == actionSync {
			fmt.Println(paint(ansiMuted, "  Окно можно закрывать — зонд работает в фоне."))
		}
	}
}

// confirmServiceTarget is the address question the service must answer before it is
// created: a background service keeps reporting wherever it was pointed, so the
// destination is shown and can be changed here. Enter keeps the current address.
func confirmServiceTarget(p prompt, configPath string, c probe.Config) (probe.Config, error) {
	fmt.Println()
	fmt.Println(paint(ansiBold, "  Куда зонд будет отправлять данные"))
	fmt.Println("  " + paint(ansiMuted, "сейчас: "+c.Server))
	updated, changed, e := rebind(p, configPath, c)
	if e != nil {
		return c, e
	}
	if !changed {
		fmt.Println(paint(ansiMuted, "  Использую адрес: "+c.Server))
		return c, nil
	}
	return updated, nil
}

// offerService asks once, right after registration, whether the probe should
// survive the console window.
func offerService(p prompt, configPath string, c probe.Config) (bool, probe.Config, error) {
	fmt.Println()
	fmt.Println(paint(ansiBold, "  Работать в фоне?"))
	fmt.Println(paint(ansiMuted, "  Служба запускается вместе с системой и продолжает работать после закрытия этого окна."))
	fmt.Println("  " + paint(ansiMagenta, "1") + "  Установить фоновую службу с автозапуском")
	fmt.Println("  " + paint(ansiMagenta, "2") + "  Остаться в консоли")
	line, e := ask(p, "Выбор (1/2): ")
	if e != nil || line != "1" {
		return false, c, nil
	}
	updated, e := confirmServiceTarget(p, configPath, c)
	if e != nil {
		return false, c, e
	}
	if e = applyServiceAction(actionInstall, configPath); e != nil {
		return false, updated, e
	}
	fmt.Println()
	fmt.Println(paint(ansiGreen, "  Готово: служба установлена и запущена, зонд работает без этого окна."))
	fmt.Println(paint(ansiMuted, "  Передумаешь — открой программу снова: статус службы и кнопка выключения будут на первом экране."))
	pause(p)
	return true, updated, nil
}

// prepareServiceConfig moves settings to service-owned paths so the background
// service never shares the spool or the Xray binary with a user profile.
func prepareServiceConfig(c *probe.Config, dir string) error {
	if c.XrayPath != "" {
		if info, statErr := os.Stat(c.XrayPath); statErr == nil && info.Mode().IsRegular() {
			xrayDest := filepath.Join(dir, filepath.Base(c.XrayPath))
			if !samePath(c.XrayPath, xrayDest) {
				if e := copyFile(c.XrayPath, xrayDest); e != nil {
					return fmt.Errorf("не удалось скопировать Xray: %w", e)
				}
			}
			c.XrayPath = xrayDest
		}
	}
	c.Spool = filepath.Join(dir, "probe-results.json")
	return nil
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

// registeredConfig loads the console settings and refuses to build a service that
// would report nowhere.
func registeredConfig(configPath string) (probe.Config, error) {
	c, e := probe.Load(configPath)
	if e != nil {
		return c, fmt.Errorf("не удалось прочитать настройки: %w", e)
	}
	if c.ProbeID == "" || len(c.Token) < 32 {
		return c, errors.New("сначала подключите зонд")
	}
	return c, nil
}

func serviceConfigPath(dir string) string {
	return filepath.Join(dir, "probe.json")
}
