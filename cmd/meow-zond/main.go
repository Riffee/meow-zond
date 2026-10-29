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
	installService := flag.Bool("install-service", false, "установить фоновую службу с автозапуском")
	uninstallService := flag.Bool("uninstall-service", false, "остановить и удалить фоновую службу")
	startService := flag.Bool("start-service", false, "запустить фоновую службу")
	stopService := flag.Bool("stop-service", false, "остановить фоновую службу")
	syncService := flag.Bool("sync-service", false, "применить настройки этого окна к службе и перезапустить её")
	enableAutostart := flag.Bool("enable-autostart", false, "включить запуск службы после перезагрузки")
	disableAutostart := flag.Bool("disable-autostart", false, "выключить запуск службы после перезагрузки")
	stopAndDisable := flag.Bool("stop-and-disable", false, "остановить службу и убрать автозапуск")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Запуск без параметров: мастер подключения и меню включения.")
		fmt.Fprintln(os.Stderr, "Адрес и имя: meow-zond -server https://status.example.com -name \"Server zond\"")
		fmt.Fprintln(os.Stderr, "Docker/headless: MEOW_SERVER=... MEOW_PROBE_NAME=... meow-zond -headless -config /data/probe.json")
		fmt.Fprintln(os.Stderr, "Фоновая служба: sudo meow-zond -install-service (Linux) или от имени администратора (Windows)")
		flag.PrintDefaults()
	}
	flag.Parse()
	action, e := requestedServiceAction(*installService, *uninstallService, *startService, *stopService, *syncService, *enableAutostart, *disableAutostart, *stopAndDisable)
	if e != nil {
		reportFatal(e, *configPath)
		os.Exit(1)
	}
	forceEnroll := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "server" || f.Name == "name" {
			forceEnroll = true
		}
	})
	o := options{
		configPath:    *configPath,
		server:        *server,
		name:          *name,
		forceEnroll:   forceEnroll,
		headless:      *headless,
		serviceAction: action,
	}
	if e = safeRun(o); e != nil {
		reportFatal(e, *configPath)
		os.Exit(1)
	}
}

type options struct {
	configPath    string
	server        string
	name          string
	forceEnroll   bool
	headless      bool
	serviceAction string
}

func requestedServiceAction(flags ...bool) (string, error) {
	names := []string{actionInstall, actionUninstall, actionStart, actionStop, actionSync, actionEnable, actionDisable, actionStopDisabled}
	requested := []string{}
	for index, on := range flags {
		if on {
			requested = append(requested, names[index])
		}
	}
	switch len(requested) {
	case 0:
		return "", nil
	case 1:
		return requested[0], nil
	default:
		return "", fmt.Errorf("выберите одно действие со службой: %s", strings.Join(requested, ", "))
	}
}

func safeRun(o options) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("внутренняя ошибка: %v\n%s", recovered, debug.Stack())
		}
	}()
	return run(o)
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

func run(o options) error {
	if o.serviceAction != "" {
		return runServiceAction(o.serviceAction, o.configPath)
	}
	var input *bufio.Reader
	if interactive() {
		input = bufio.NewReader(os.Stdin)
	}
	c, loadErr := probe.Load(o.configPath)
	fresh := false
	if o.forceEnroll || loadErr != nil {
		if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
			fmt.Fprintln(os.Stderr, "Текущая настройка зонда будет заменена.")
		}
		embedded, hasEmbedded := distribution.ReadExecutable()
		if strings.TrimSpace(o.server) == "" && hasEmbedded {
			o.server = embedded.Server
		}
		server := strings.TrimSpace(o.server)
		name := strings.TrimSpace(o.name)
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
		c.Spool = filepath.Join(filepath.Dir(o.configPath), "probe-results.json")
		if e = probe.Save(o.configPath, c); e != nil {
			return fmt.Errorf("не удалось сохранить настройки: %w", e)
		}
		fresh = true
		fmt.Printf("Зонд «%s» подключён. Настройки: %s\n", displayName(c), o.configPath)
	}

	if handled, e := runService(c); handled {
		if e != nil {
			return fmt.Errorf("служба Windows завершилась с ошибкой: %w", e)
		}
		return nil
	} else if e != nil {
		fmt.Fprintln(os.Stderr, "Проверка режима службы Windows:", e)
	}
	if o.headless || !interactive() {
		if serviceInstalled() {
			return errors.New("зонд уже работает как фоновая служба: остановите её интерактивной программой, systemctl или services.msc")
		}
		return runHeadless(c)
	}
	if fresh && serviceSupported() && !serviceInstalled() {
		done, updated, e := offerService(readerPrompt(input), o.configPath, c)
		c = updated
		if e != nil {
			fmt.Fprintln(os.Stderr, "Фоновая служба:", e)
		} else if done {
			return nil
		}
	}
	return console(o.configPath, c, input)
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

// prompt reads one console line. The menu loop owns a single reader goroutine, so
// prompts inside the menu consume its channel instead of racing that goroutine.
type prompt func() (string, error)

func readerPrompt(input *bufio.Reader) prompt {
	return func() (string, error) { return readLine(input) }
}

func channelPrompt(lines <-chan string) prompt {
	return func() (string, error) {
		line, ok := <-lines
		if !ok {
			return "", io.EOF
		}
		return line, nil
	}
}

func ask(p prompt, label string) (string, error) {
	fmt.Print("  " + paint(ansiBold, label))
	return p()
}

func pause(p prompt) {
	fmt.Print("\n  " + paint(ansiMuted, "Нажмите Enter…"))
	_, _ = p()
}

// rebind connects the probe to another status-page address and replaces the saved
// settings, keeping local paths and check endpoints. The name is asked because the
// panel shows the probe under it on the new server.
func rebind(p prompt, configPath string, c probe.Config) (probe.Config, bool, error) {
	fmt.Println()
	fmt.Println(paint(ansiBold, "  Адрес статус-страницы"))
	fmt.Println("  " + paint(ansiMuted, "сейчас: "+c.Server+" · зонд «"+displayName(c)+"»"))
	server, e := ask(p, "новый адрес (Enter — отмена): ")
	if e != nil {
		return c, false, e
	}
	server = strings.TrimSpace(server)
	if server == "" {
		fmt.Println(paint(ansiMuted, "  Отменено."))
		return c, false, nil
	}
	if server == c.Server {
		fmt.Println(paint(ansiMuted, "  Адрес уже такой. Настройки не изменены."))
		return c, false, nil
	}
	if strings.HasPrefix(strings.ToLower(server), "http://") {
		fmt.Println(paint(ansiYellow, "  Внимание: HTTP без шифрования допустим только для локальной отладки."))
	}
	name := displayName(c)
	if strings.TrimSpace(c.ProbeName) != "" {
		name = c.ProbeName
	}
	entered, e := ask(p, "имя зонда ["+name+"]: ")
	if e != nil {
		return c, false, e
	}
	if entered = strings.TrimSpace(entered); entered != "" {
		name = entered
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	nc, e := probe.Register(ctx, server, name)
	if e != nil {
		return c, false, e
	}
	carryLocalSettings(c, &nc)
	if e = probe.Save(configPath, nc); e != nil {
		return nc, false, fmt.Errorf("адрес проверен, но настройки не сохранены: %w", e)
	}
	fmt.Println(paint(ansiGreen, "  Подключено: «"+displayName(nc)+"» → "+nc.Server))
	return nc, true, nil
}

// carryLocalSettings keeps paths, control endpoints and the expected interface
// when the probe is registered again, so only identity and server change.
func carryLocalSettings(from probe.Config, to *probe.Config) {
	to.Spool = from.Spool
	to.XrayPath = from.XrayPath
	to.XraySHA256 = from.XraySHA256
	to.IdentityURL = from.IdentityURL
	if len(from.IdentityURLs) > 0 {
		to.IdentityURLs = from.IdentityURLs
	}
	if len(from.Controls) > 0 {
		to.Controls = from.Controls
	}
	to.ExpectedInterface = from.ExpectedInterface
}

// renameProbe changes the name in place. If the panel no longer knows this
// probe (the user deleted or revoked it), the same name is registered again and
// the fresh token replaces the dead one instead of leaving the menu stuck with
// "backend HTTP 401".
func renameProbe(p prompt, configPath string, c probe.Config) (probe.Config, bool, error) {
	fmt.Println()
	fmt.Println(paint(ansiBold, "  Имя зонда"))
	fmt.Println("  " + paint(ansiMuted, "сейчас: «"+displayName(c)+"»"))
	entered, e := ask(p, "новое имя (Enter — отмена): ")
	if e != nil {
		return c, false, e
	}
	name := strings.TrimSpace(entered)
	if name == "" {
		fmt.Println(paint(ansiMuted, "  Отменено."))
		return c, false, nil
	}
	if len(name) > 100 {
		return c, false, fmt.Errorf("имя должно быть короче 100 символов")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if c.ProbeID == "" || len(c.Token) < 32 {
		return registerAgain(ctx, configPath, c, name)
	}
	if e = probe.Rename(ctx, c, name); e != nil {
		if !probe.IsUnauthorized(e) {
			return c, false, e
		}
		fmt.Println("  " + paint(ansiYellow, "Панель больше не знает этот зонд: он удалён или доступ отозван."))
		answer, e := ask(p, "Подключить заново с новым именем? (Y/n): ")
		if e != nil {
			return c, false, e
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "", "y", "yes", "д", "да":
			return registerAgain(ctx, configPath, c, name)
		default:
			fmt.Println(paint(ansiMuted, "  Оставлено старое имя и старый доступ."))
			return c, false, nil
		}
	}
	c.ProbeName = name
	if e = probe.Save(configPath, c); e != nil {
		return c, false, fmt.Errorf("имя изменено на сервере, но не сохранено локально: %w", e)
	}
	fmt.Println(paint(ansiGreen, "  Теперь зонд называется «"+name+"»."))
	return c, true, nil
}

func registerAgain(ctx context.Context, configPath string, c probe.Config, name string) (probe.Config, bool, error) {
	nc, e := probe.Register(ctx, c.Server, name)
	if e != nil {
		return c, false, e
	}
	carryLocalSettings(c, &nc)
	if e = probe.Save(configPath, nc); e != nil {
		return nc, false, fmt.Errorf("зонд подключён, но настройки не сохранены: %w", e)
	}
	fmt.Println(paint(ansiGreen, "  Зонд зарегистрирован заново: «"+displayName(nc)+"» → "+nc.Server))
	fmt.Println(paint(ansiMuted, "  Назначьте новый зонд мониторам в админке."))
	return nc, true, nil
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
	askLine := channelPrompt(lines)

	var runner *probe.Runner
	var runCancel context.CancelFunc
	var done chan error
	notice := ""
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

	start := func() error {
		if serviceRunning() {
			return errors.New("зонд уже работает как фоновая служба — остановите её в пункте 5")
		}
		r, e := probe.New(c)
		if e != nil {
			return e
		}
		runCtx, stopRun := context.WithCancel(ctx)
		finished := make(chan error, 1)
		runner, runCancel, done = r, stopRun, finished
		go func() { finished <- r.RunWithLog(runCtx, io.Discard) }()
		return nil
	}

	lastFrame := -1
	draw := func(refresh bool) {
		frame := screenFrame(c, runner, notice, serviceSupported())
		if refresh && len(frame) == lastFrame {
			refreshScreen(frame)
			return
		}
		writeScreen(frame)
		lastFrame = len(frame)
	}
	draw(false)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if !inputPending() {
				draw(true)
			}
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
			notice = ""
			switch line {
			case "1":
				if runner == nil {
					if e := start(); e != nil {
						notice = paint(ansiRed, "Не удалось включить: "+e.Error())
						break
					}
					notice = paint(ansiGreen, "Зонд включён. Проверяю связь…")
					select {
					case <-time.After(2 * time.Second):
					case <-ctx.Done():
					}
				} else {
					stop()
					notice = paint(ansiMuted, "Зонд выключен: данные не отправляются.")
				}
			case "2":
			case "3":
				updated, changed, e := renameProbe(askLine, configPath, c)
				if e != nil {
					notice = paint(ansiRed, "Имя не изменено: "+e.Error())
					break
				}
				if !changed {
					break
				}
				keepLocalRunner := runner != nil
				stop()
				c = updated
				if serviceInstalled() {
					if e = applyServiceAction("sync-service", configPath); e != nil {
						notice = paint(ansiRed, "Служба работает со старым именем: "+e.Error())
						break
					}
					notice = paint(ansiGreen, "Служба получила новое имя и перезапущена.")
					break
				}
				if keepLocalRunner {
					if e = start(); e != nil {
						notice = paint(ansiRed, "Не удалось включить зонд после смены имени: "+e.Error())
					}
				}
			case "4":
				updated, changed, e := rebind(askLine, configPath, c)
				if e != nil {
					notice = paint(ansiRed, "Адрес не изменён: "+e.Error())
					break
				}
				if !changed {
					break
				}
				keepLocalRunner := runner != nil
				stop()
				c = updated
				if serviceInstalled() {
					if e = applyServiceAction("sync-service", configPath); e != nil {
						notice = paint(ansiRed, "Служба работает со старыми настройками: "+e.Error())
						break
					}
					notice = paint(ansiGreen, "Служба получила новые настройки и перезапущена.")
					break
				}
				if keepLocalRunner {
					if e = start(); e != nil {
						notice = paint(ansiRed, "Не удалось включить зонд после смены адреса: "+e.Error())
					}
				}
			case "5":
				updated := serviceMenu(askLine, configPath, c)
				c = updated
				if runner != nil && serviceRunning() {
					stop()
					notice = paint(ansiMuted, "Зонд в этом окне выключен — работает фоновая служба.")
				}
			case "0", "q", "exit":
				stop()
				fmt.Println("\n" + paint(ansiMuted, "Зонд выключен."))
				return nil
			case "":
			}
			draw(false)
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
