package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	"meowzond/internal/probe"
)

var (
	colorEnabled  bool
	screenEnabled bool
)

const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiDim     = "\x1b[2m"
	ansiMagenta = "\x1b[38;5;176m"
	ansiGreen   = "\x1b[38;5;84m"
	ansiYellow  = "\x1b[38;5;221m"
	ansiRed     = "\x1b[38;5;203m"
	ansiMuted   = "\x1b[38;5;245m"
)

func initUI() {
	tty := isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
	colorEnabled = tty && os.Getenv("NO_COLOR") == ""
	screenEnabled = tty
}

func paint(code, value string) string {
	if !colorEnabled || value == "" {
		return value
	}
	return code + value + ansiReset
}

func clearScreen() {
	if screenEnabled {
		fmt.Print("\x1b[H\x1b[2J")
	}
}

func rule(width int) string {
	if width < 16 {
		width = 16
	}
	return paint(ansiMagenta, strings.Repeat("─", width))
}

func printBanner(c probe.Config, configPath string) {
	fmt.Println(paint(ansiMagenta, "  /\\_/\\   ") + paint(ansiBold, "meow! probe"))
	fmt.Println(paint(ansiMagenta, " ( o.o )  ") + displayName(c))
	fmt.Println(paint(ansiMagenta, "  > ^ <   ") + paint(ansiMuted, c.Server))
	fmt.Println(rule(50))
	if c.Development {
		fmt.Println(paint(ansiYellow, "  Локальный режим: соединение без HTTPS не защищено"))
	}
	fmt.Println()
}

func printMenu(enabled bool) {
	fmt.Println()
	fmt.Println(paint(ansiMuted, "  Действия"))
	if enabled {
		fmt.Println("  " + paint(ansiMagenta, "1") + "  Выключить зонд")
		fmt.Println("  " + paint(ansiMagenta, "2") + "  Обновить статус")
		fmt.Println("  " + paint(ansiMagenta, "0") + "  Выключить и выйти")
	} else {
		fmt.Println("  " + paint(ansiMagenta, "1") + "  Включить зонд")
		fmt.Println("  " + paint(ansiMagenta, "2") + "  Показать статус")
		fmt.Println("  " + paint(ansiMagenta, "0") + "  Выход")
	}
	fmt.Println(paint(ansiMuted, "  Ctrl+C — выключить и выйти"))
	fmt.Print("\n  " + paint(ansiBold, "Выбор: "))
}

func printRunnerStatus(r *probe.Runner) {
	s := r.Status()
	o := s.Observation
	fmt.Println()
	fmt.Println(paint(ansiMuted, "  Состояние"))
	state := paint(ansiGreen, "ВКЛЮЧЁН")
	if !s.Connected {
		state = paint(ansiYellow, "ПОДКЛЮЧАЕТСЯ")
		if s.LastError != "" {
			state = paint(ansiRed, "ОШИБКА СВЯЗИ")
		}
	}
	fmt.Printf("  %-12s %s\n", paint(ansiMuted, "зонд"), state)

	if s.Connected {
		fmt.Printf("  %-12s %s\n", paint(ansiMuted, "связь"), paint(ansiGreen, "OK")+paint(ansiMuted, ", "+ago(s.LastContact)))
	} else if s.LastError != "" {
		fmt.Printf("  %-12s %s\n", paint(ansiMuted, "связь"), paint(ansiRed, s.LastError))
	}
	if o.Healthy {
		fmt.Printf("  %-12s %s\n", paint(ansiMuted, "интернет"), paint(ansiGreen, "OK"))
	} else if s.LastContact > 0 {
		fmt.Printf("  %-12s %s\n", paint(ansiMuted, "интернет"), paint(ansiRed, "контроль не проходит"))
	}
	if o.IP != "" {
		network := o.IP
		if o.Country != "" {
			network += ", " + o.Country
		}
		if o.ASN > 0 {
			network += fmt.Sprintf(", AS%d", o.ASN)
		}
		fmt.Printf("  %-12s %s\n", paint(ansiMuted, "сеть"), network)
		provider := o.Operator
		if o.City != "" {
			if provider != "" {
				provider += ", " + o.City
			} else {
				provider = o.City
			}
		}
		if provider != "" {
			fmt.Printf("  %-12s %s\n", paint(ansiMuted, "провайдер"), provider)
		}
	} else {
		fmt.Printf("  %-12s %s\n", paint(ansiMuted, "сеть"), paint(ansiMuted, "определяется…"))
	}
	switch {
	case o.VPN && o.Direct:
		route := paint(ansiYellow, "используется исходная сеть")
		if o.Interface != "" {
			route += paint(ansiMuted, ", "+o.Interface)
		}
		route += paint(ansiMuted, ", VPN активен")
		fmt.Printf("  %-12s %s\n", paint(ansiMuted, "маршрут"), route)
	case o.VPN:
		fmt.Printf("  %-12s %s\n", paint(ansiMuted, "маршрут"), paint(ansiRed, "VPN перехватывает трафик, исходный маршрут недоступен"))
		if o.PathError != "" {
			fmt.Printf("  %-12s %s\n", paint(ansiMuted, ""), paint(ansiMuted, o.PathError))
		}
	case o.Interface != "":
		fmt.Printf("  %-12s %s\n", paint(ansiMuted, "маршрут"), paint(ansiGreen, "прямое подключение")+paint(ansiMuted, ", "+o.Interface))
	default:
		if o.PathError != "" {
			fmt.Printf("  %-12s %s\n", paint(ansiMuted, "маршрут"), paint(ansiYellow, o.PathError))
		}
	}
	if s.Pending > 0 {
		fmt.Printf("  %-12s %d\n", paint(ansiMuted, "очередь"), s.Pending)
	}
}

func printOffStatus() {
	fmt.Println()
	fmt.Println(paint(ansiMuted, "  Состояние"))
	fmt.Printf("  %-12s %s\n", paint(ansiMuted, "зонд"), paint(ansiMuted, "ВЫКЛЮЧЕН"))
	fmt.Println(paint(ansiMuted, "  Данные не отправляются, пока зонд выключен."))
}
