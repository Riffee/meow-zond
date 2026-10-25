package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/mattn/go-isatty"
	"io"
	"meowzond/internal/distribution"
	"meowzond/internal/probe"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
)

func main() {
	prepareConsole()
	initUI()
	configPath := flag.String("config", defaultConfigPath(), "путь к файлу настроек зонда")
	server := flag.String("server", os.Getenv("MEOW_SERVER"), "адрес статус-страницы для подключения")
	name := flag.String("name", os.Getenv("MEOW_PROBE_NAME"), "имя зонда для автоматической регистрации")
	headless := flag.Bool("headless", false, "работать без консольного меню")
	installService := flag.Bool("install-service", false, "установить фоновую службу Windows")
	uninstallService := flag.Bool("uninstall-service", false, "удалить фоновую службу Windows")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Запуск без параметров: мастер подключения и меню включения.")
		fmt.Fprintln(os.Stderr, "Адрес и имя: meow-zond -server https://status.example.com -name \"Server zond\"")
		fmt.Fprintln(os.Stderr, "Docker/headless: MEOW_SERVER=... MEOW_PROBE_NAME=... meow-zond -headless -config /data/probe.json")
		flag.PrintDefaults()
	}
	flag.Parse()
	forceEnroll := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "server" || f.Name == "name" {
			forceEnroll = true
		}
	})
	if e := safeRun(*configPath, *server, *name, forceEnroll, *headless, *installService, *uninstallService); e != nil {
		reportFatal(e, *configPath)
		os.Exit(1)
	}
}

func safeRun(configPath, server, name string, forceEnroll, headless, installService, uninstallService bool) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("внутренняя ошибка: %v\n%s", recovered, debug.Stack())
		}
	}()
	return run(configPath, server, name, forceEnroll, headless, installService, uninstallService)
}

func reportFatal(err error, configPath string) {
	fmt.Fprintln(os.Stderr, "Ошибка:", err)
	dir := filepath.Dir(configPath)
	if configPath == "" {
		dir = appDataDir()
	}
	if dir == "." {
		dir = appDataDir()
	}
	_ = os.MkdirAll(dir, 0700)
	path := filepath.Join(dir, "meow-probe-error.log")
	if f, e := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600); e == nil {
		_, _ = fmt.Fprintf(f, "%s %v\n", time.Now().Format(time.RFC3339), err)
		_ = f.Close()
	}
	if interactive() {
		fmt.Print("\nНажмите Enter, чтобы закрыть окно…")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}
}

func appDataDir() string {
	if runtime.GOOS == "windows" {
		if base := os.Getenv("LOCALAPPDATA"); base != "" {
			return filepath.Join(base, "MeowProbe")
		}
	}
	if base, e := os.UserConfigDir(); e == nil {
		return filepath.Join(base, "meow-probe")
	}
	if home, e := os.UserHomeDir(); e == nil {
		return filepath.Join(home, ".meow-probe")
	}
	return "."
}

func defaultConfigPath() string {
	return filepath.Join(appDataDir(), "probe.json")
}

func run(configPath, server, name string, forceEnroll, headless, installService, uninstallService bool) error {
	if installService && uninstallService {
		return errors.New("нельзя одновременно установить и удалить службу")
	}
	if uninstallService {
		return uninstallWindowsService()
	}
	var input *bufio.Reader
	if interactive() {
		input = bufio.NewReader(os.Stdin)
	}
	c, loadErr := probe.Load(configPath)
	if forceEnroll || loadErr != nil {
		if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
			fmt.Fprintln(os.Stderr, "Текущая настройка зонда будет заменена.")
		}
		embedded, hasEmbedded := distribution.ReadExecutable()
		if strings.TrimSpace(server) == "" && hasEmbedded {
			server = embedded.Server
		}
		server = strings.TrimSpace(server)
		name = strings.TrimSpace(name)
		if server == "" {
			if input == nil {
				return errors.New("укажите адрес статус-страницы: -server или MEOW_SERVER")
			}
			fmt.Print("\n  " + paint(ansiBold, "Адрес статус-страницы: "))
			var e error
			if server, e = readLine(input); e != nil {
				return e
			}
			server = strings.TrimSpace(server)
		}
		if name == "" {
			if input == nil {
				return errors.New("укажите имя зонда: -name или MEOW_PROBE_NAME")
			}
			fmt.Print("\n  " + paint(ansiBold, "Как вас подписать в панели: "))
			var e error
			if name, e = readLine(input); e != nil {
				return e
			}
			name = strings.TrimSpace(name)
		}
		if server == "" {
			return errors.New("адрес статус-страницы не указан")
		}
		if name == "" {
			return errors.New("имя зонда не указано")
		}
		if strings.HasPrefix(strings.ToLower(server), "http://") {
			fmt.Fprintln(os.Stderr, "Внимание: HTTP без шифрования допустим только для локальной отладки.")
		}
		var e error
		c, e = probe.Register(context.Background(), server, name)
		if e != nil {
			return e
		}
		c.Spool = filepath.Join(filepath.Dir(configPath), "probe-results.json")
		if e = probe.Save(configPath, c); e != nil {
			return fmt.Errorf("не удалось сохранить настройки: %w", e)
		}
		fmt.Printf("Зонд «%s» подключён. Настройки: %s\n", displayName(c), configPath)
	}

	if handled, e := runService(c); handled {
		if e != nil {
			return fmt.Errorf("служба Windows завершилась с ошибкой: %w", e)
		}
		return nil
	} else if e != nil {
		fmt.Fprintln(os.Stderr, "Проверка режима службы Windows:", e)
	}
	if installService {
		if e := installWindowsService(configPath); e != nil {
			return e
		}
		fmt.Println("Служба установлена и запущена. Консольное окно можно закрыть.")
		return nil
	}
	if !headless && input != nil {
		handled, e := windowsServicePrompt(input, configPath)
		if e != nil {
			fmt.Fprintln(os.Stderr, "Служба Windows:", e)
		} else if handled {
			return nil
		}
	}
	if headless || !interactive() {
		if windowsServiceInstalled() {
			return fmt.Errorf("зонд уже установлен как служба Windows; управление доступно в интерактивном режиме или через services.msc")
		}
		return runHeadless(c)
	}
	return console(configPath, c, input)
}

func readLine(input *bufio.Reader) (string, error) {
	line, e := input.ReadString('\n')
	if e != nil && !errors.Is(e, io.EOF) {
		return "", e
	}
	line = strings.TrimSpace(line)
	if line == "" && errors.Is(e, io.EOF) {
		return "", io.EOF
	}
	return line, nil
}

func interactive() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
}

func displayName(c probe.Config) string {
	if strings.TrimSpace(c.ProbeName) != "" {
		return c.ProbeName
	}
	if len(c.ProbeID) > 8 {
		return "зонд " + c.ProbeID[:8]
	}
	return "зонд"
}

func runHeadless(c probe.Config) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	logPath := filepath.Join(filepath.Dir(c.Spool), "probe.log")
	logger := newBoundedLog(os.Stdout, logPath, 500)
	fmt.Fprintf(logger, "%s probe «%s» → %s\n", time.Now().Format("15:04:05"), displayName(c), c.Server)
	r, e := probe.New(c)
	if e != nil {
		return e
	}
	return r.RunWithLog(ctx, logger)
}

func console(configPath string, c probe.Config, input *bufio.Reader) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	lines := make(chan string)
	go func() {
		for {
			line, e := input.ReadString('\n')
			if e != nil && !errors.Is(e, io.EOF) {
				close(lines)
				return
			}
			lines <- strings.TrimSpace(line)
			if errors.Is(e, io.EOF) {
				close(lines)
				return
			}
		}
	}()

	var runner *probe.Runner
	var runCancel context.CancelFunc
	var done chan error
	stop := func() {
		if runner == nil {
			return
		}
		runCancel()
		<-done
		runner = nil
		runCancel = nil
		done = nil
	}
	defer stop()

	draw := func() {
		clearScreen()
		printBanner(c, configPath)
		if runner == nil {
			printOffStatus()
		} else {
			printRunnerStatus(runner)
		}
		printMenu(runner != nil)
	}
	draw()
	for {
		select {
		case <-ctx.Done():
			stop()
			fmt.Println("\n" + paint(ansiMuted, "Зонд выключен."))
			return nil
		case line, ok := <-lines:
			if !ok {
				stop()
				fmt.Println("\n" + paint(ansiMuted, "Зонд выключен."))
				return nil
			}
			switch line {
			case "1":
				if runner == nil {
					r, e := probe.New(c)
					if e != nil {
						clearScreen()
						printBanner(c, configPath)
						printOffStatus()
						fmt.Println(paint(ansiRed, "Не удалось включить: "+e.Error()))
						printMenu(false)
						continue
					}
					runCtx, stopRun := context.WithCancel(ctx)
					finished := make(chan error, 1)
					runner, runCancel, done = r, stopRun, finished
					go func() { finished <- r.RunWithLog(runCtx, io.Discard) }()
					clearScreen()
					printBanner(c, configPath)
					fmt.Println()
					fmt.Println(paint(ansiGreen, "  Зонд включён. Проверяю связь…"))
					select {
					case <-time.After(2 * time.Second):
					case <-ctx.Done():
					}
				} else {
					stop()
				}
			case "2":
			case "0", "q", "exit":
				stop()
				fmt.Println(paint(ansiMuted, "Зонд выключен."))
				return nil
			case "":
			}
			draw()
		}
	}
}

func ago(timestamp int64) string {
	if timestamp <= 0 {
		return "ещё не было обмена"
	}
	seconds := time.Now().Unix() - timestamp
	if seconds < 5 {
		return "только что"
	}
	if seconds < 60 {
		return fmt.Sprintf("%d сек. назад", seconds)
	}
	return fmt.Sprintf("%d мин. назад", seconds/60)
}
