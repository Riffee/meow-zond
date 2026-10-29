package main

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

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

func rule(width int) string {
	if width < 16 {
		width = 16
	}
	return paint(ansiMagenta, strings.Repeat("─", width))
}

func label(value string) string {
	padding := 12 - utf8.RuneCountInString(value)
	if padding < 1 {
		padding = 1
	}
	return paint(ansiMuted, value+strings.Repeat(" ", padding))
}

// terminalSize returns the visible console window, not the scrollback buffer.
// A failed query keeps the classic 80x25 size, so a redirected or unusual
// terminal still gets a stable frame.
func terminalSize() (int, int) {
	width, height := consoleSize()
	if width < 20 {
		width = 80
	}
	if height < 8 {
		height = 25
	}
	return width, height
}

// visibleWidth counts runes while skipping ANSI colour sequences, so a line can
// be cut to the terminal width without losing the rest of the frame.
func visibleWidth(value string) int {
	width := 0
	for i := 0; i < len(value); {
		if value[i] == 0x1b {
			i = skipANSI(value, i)
			continue
		}
		_, size := decodeRune(value[i:])
		width++
		i += size
	}
	return width
}

func skipANSI(value string, start int) int {
	i := start + 1
	if i < len(value) && value[i] == '[' {
		i++
		for i < len(value) {
			c := value[i]
			i++
			if c >= 0x40 && c <= 0x7e {
				break
			}
		}
		return i
	}
	if i < len(value) {
		i++
	}
	return i
}

func decodeRune(value string) (rune, int) {
	return utf8.DecodeRuneInString(value)
}

// fitLine truncates a coloured line to the window width and always closes the
// last colour sequence, so a long server URL or backend error cannot wrap and
// scroll the menu.
func fitLine(value string, width int) string {
	value = strings.TrimRight(value, "\r\n")
	if width < 2 {
		width = 2
	}
	if visibleWidth(value) < width {
		return value
	}
	target := width - 2
	if target < 0 {
		target = 0
	}
	var out strings.Builder
	visible := 0
	for i := 0; i < len(value); {
		if value[i] == 0x1b {
			next := skipANSI(value, i)
			out.WriteString(value[i:next])
			i = next
			continue
		}
		r, size := decodeRune(value[i:])
		if visible >= target {
			break
		}
		out.WriteRune(r)
		visible++
		i += size
	}
	out.WriteString("…")
	if colorEnabled {
		out.WriteString(ansiReset)
	}
	return out.String()
}

func writeScreen(lines []string) {
	width, height := terminalSize()
	lines = fitFrame(lines, width, height)
	if !screenEnabled {
		for _, line := range lines {
			fmt.Println(line)
		}
		return
	}
	fmt.Print("\x1b[H\x1b[2J")
	for index, line := range lines {
		if index > 0 {
			fmt.Print("\r\n")
		}
		fmt.Print(fitLine(line, width))
	}
}

// refreshScreen updates every line above the prompt and restores the cursor.
// Text the user has already typed on the prompt line stays untouched.
func refreshScreen(lines []string) {
	if !screenEnabled || len(lines) < 2 {
		return
	}
	width, height := terminalSize()
	lines = fitFrame(lines, width, height)
	body := lines[:len(lines)-1]
	fmt.Print("\x1b[s\x1b[H")
	for index, line := range body {
		if index > 0 {
			fmt.Print("\r\n")
		}
		fmt.Print("\x1b[2K")
		fmt.Print(fitLine(line, width))
	}
	fmt.Print("\x1b[u")
}

func fitFrame(lines []string, width, height int) []string {
	limit := height - 1
	if limit < 2 {
		limit = 2
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, fitLine(line, width))
		if len(out) == limit {
			break
		}
	}
	return out
}

func promptLine() string {
	return "  " + paint(ansiBold, "Выбор: ")
}

func noticeLines(notice string) []string {
	if strings.TrimSpace(notice) == "" {
		return nil
	}
	lines := []string{}
	for _, line := range strings.Split(notice, "\n") {
		lines = append(lines, "  "+line)
	}
	return lines
}

// screenFrame composes a full, compact or tiny menu that fits the visible
// window. The menu and the prompt are required lines and are never dropped.
func screenFrame(c probe.Config, runner *probe.Runner, notice string, serviceSupported bool) []string {
	width, height := terminalSize()
	return screenFrameAt(c, runner, notice, serviceSupported, width, height)
}

func screenFrameAt(c probe.Config, runner *probe.Runner, notice string, serviceSupported bool, width, height int) []string {
	if width < 20 {
		width = 80
	}
	limit := height - 1
	if limit < 6 {
		limit = 6
	}
	if height >= 22 {
		if lines := fullFrame(c, runner, notice, serviceSupported, limit); len(lines) <= limit {
			return lines
		}
	}
	if lines := compactFrame(c, runner, notice, serviceSupported, limit); len(lines) <= limit {
		return lines
	}
	return tinyFrame(c, runner, notice, serviceSupported, limit)
}

type frameLine struct {
	text  string
	level int
}

// trimFrame drops optional detail lines before the menu, so a long provider
// name or a notice never pushes the top of the screen out of the window.
func trimFrame(lines []frameLine, limit int) []string {
	for len(lines) > limit {
		highest := 0
		for _, line := range lines {
			if line.level > highest {
				highest = line.level
			}
		}
		if highest == 0 {
			break
		}
		for index, line := range lines {
			if line.level == highest {
				lines = append(lines[:index], lines[index+1:]...)
				break
			}
		}
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, line.text)
	}
	return out
}

func fullFrame(c probe.Config, runner *probe.Runner, notice string, serviceSupported bool, limit int) []string {
	lines := []frameLine{
		{"  " + paint(ansiMagenta, "/\\_/\\   ") + paint(ansiBold, "meow! probe"), 0},
		{" ( o.o )  " + displayName(c), 0},
		{"  > ^ <   " + paint(ansiMuted, c.Server), 0},
		{rule(54), 1},
		{"", 1},
		{"  " + paint(ansiMuted, "Состояние"), 1},
	}
	status := statusFrame(runner)
	lines = append(lines, status...)
	lines = append(lines, frameLine{"", 1})
	for _, line := range serviceSummaryFrame() {
		lines = append(lines, frameLine{line, 1})
	}
	if notice != "" {
		lines = append(lines, frameLine{"", 1})
		for _, line := range noticeLines(notice) {
			lines = append(lines, frameLine{line, 0})
		}
	}
	lines = append(lines, frameLine{"", 1})
	for _, line := range fullMenuFrame(runner != nil, serviceSupported) {
		lines = append(lines, frameLine{line, 0})
	}
	return trimFrame(lines, limit)
}

func compactFrame(c probe.Config, runner *probe.Runner, notice string, serviceSupported bool, limit int) []string {
	lines := []frameLine{
		{"  " + paint(ansiMagenta, "/\\_/\\") + " " + paint(ansiBold, "meow! probe") + paint(ansiMuted, " · "+displayName(c)), 0},
		{"  " + paint(ansiMuted, c.Server), 1},
		{rule(54), 1},
		{"", 1},
		{"  " + paint(ansiBold, "Состояние"), 1},
	}
	for _, line := range compactStatusFrame(runner) {
		lines = append(lines, frameLine{line.text, 0})
	}
	for _, line := range serviceSummaryFrame() {
		lines = append(lines, frameLine{line, 1})
	}
	if notice != "" {
		lines = append(lines, frameLine{"", 1})
		for _, line := range noticeLines(notice) {
			lines = append(lines, frameLine{line, 0})
		}
	}
	lines = append(lines, frameLine{"", 1})
	for _, line := range compactMenuFrame(runner != nil, serviceSupported) {
		lines = append(lines, frameLine{line, 0})
	}
	return trimFrame(lines, limit)
}

func tinyFrame(c probe.Config, runner *probe.Runner, notice string, serviceSupported bool, limit int) []string {
	lines := []frameLine{
		{"  " + paint(ansiBold, "meow! probe") + paint(ansiMuted, " · "+displayName(c)), 0},
		{"  " + paint(ansiMuted, c.Server), 1},
	}
	for _, line := range compactStatusFrame(runner) {
		lines = append(lines, frameLine{line.text, 0})
	}
	if notice != "" {
		for _, line := range noticeLines(notice) {
			lines = append(lines, frameLine{line, 0})
		}
	}
	first := []string{"1 Включить", "2 Обновить", "3 Имя"}
	if runner != nil {
		first[0] = "1 Выключить"
	}
	second := []string{"4 Адрес"}
	if serviceSupported {
		second = append(second, "5 Служба")
	}
	second = append(second, "0 Выход")
	lines = append(lines, frameLine{"", 1})
	lines = append(lines, frameLine{"  " + paint(ansiMuted, strings.Join(first, " · ")), 0})
	lines = append(lines, frameLine{"  " + paint(ansiMuted, strings.Join(second, " · ")), 0})
	lines = append(lines, frameLine{promptLine(), 0})
	return trimFrame(lines, limit)
}

func statusFrame(r *probe.Runner) []frameLine {
	if r == nil {
		return offStatusFrame(serviceRunning())
	}
	s := r.Status()
	o := s.Observation
	state := paint(ansiGreen, "ВКЛЮЧЁН")
	switch {
	case s.Unauthorized:
		state = paint(ansiRed, "ДОСТУП ОТОЗВАН")
	case !s.Connected && s.LastError != "":
		state = paint(ansiRed, "ОШИБКА СВЯЗИ")
	case !s.Connected:
		state = paint(ansiYellow, "ПОДКЛЮЧАЕТСЯ")
	}
	lines := []frameLine{{"  " + label("зонд") + state, 0}}
	if s.Unauthorized {
		lines = append(lines, frameLine{"  " + paint(ansiYellow, "Зонд удалён из панели. Пункт 3 — имя и повторное подключение."), 0})
	}
	if s.Connected {
		lines = append(lines, frameLine{"  " + label("связь") + paint(ansiGreen, "OK") + paint(ansiMuted, ", "+ago(s.LastContact)), 0})
	} else if s.Unauthorized {
		lines = append(lines, frameLine{"  " + label("связь") + paint(ansiRed, "доступ отозван"), 0})
	} else if s.LastError != "" {
		lines = append(lines, frameLine{"  " + label("связь") + paint(ansiRed, s.LastError), 1})
	}
	if o.Healthy {
		lines = append(lines, frameLine{"  " + label("интернет") + paint(ansiGreen, "OK"), 1})
	} else if s.LastContact > 0 {
		lines = append(lines, frameLine{"  " + label("интернет") + paint(ansiRed, "контроль не проходит"), 1})
	}
	if o.IP != "" {
		network := o.IP
		if o.Country != "" {
			network += ", " + o.Country
		}
		if o.ASN > 0 {
			network += fmt.Sprintf(", AS%d", o.ASN)
		}
		lines = append(lines, frameLine{"  " + label("сеть") + network, 0})
		provider := o.Operator
		if o.City != "" {
			if provider != "" {
				provider += ", " + o.City
			} else {
				provider = o.City
			}
		}
		if provider != "" {
			lines = append(lines, frameLine{"  " + label("провайдер") + provider, 3})
		}
	} else {
		lines = append(lines, frameLine{"  " + label("сеть") + paint(ansiMuted, "определяется…"), 2})
	}
	switch {
	case o.VPN && o.Direct:
		route := paint(ansiYellow, "исходная сеть")
		if o.Interface != "" {
			route += paint(ansiMuted, ", "+o.Interface)
		}
		route += paint(ansiMuted, ", VPN активен")
		lines = append(lines, frameLine{"  " + label("маршрут") + route, 1})
	case o.VPN:
		lines = append(lines, frameLine{"  " + label("маршрут") + paint(ansiRed, "VPN перехватывает трафик, исходный маршрут недоступен"), 1})
		if o.PathError != "" {
			lines = append(lines, frameLine{"  " + label("") + paint(ansiMuted, o.PathError), 3})
		}
	case o.Interface != "":
		lines = append(lines, frameLine{"  " + label("маршрут") + paint(ansiGreen, "прямое подключение") + paint(ansiMuted, ", "+o.Interface), 1})
	default:
		if o.PathError != "" {
			lines = append(lines, frameLine{"  " + label("маршрут") + paint(ansiYellow, o.PathError), 2})
		}
	}
	if s.Pending > 0 {
		lines = append(lines, frameLine{"  " + label("очередь") + fmt.Sprintf("%d", s.Pending), 3})
	}
	return lines
}

func compactStatusFrame(r *probe.Runner) []frameLine {
	if r == nil {
		return offStatusFrame(serviceRunning())
	}
	s := r.Status()
	o := s.Observation
	state := paint(ansiGreen, "ВКЛЮЧЁН")
	connection := paint(ansiGreen, "связь OK")
	switch {
	case s.Unauthorized:
		state = paint(ansiRed, "ДОСТУП ОТОЗВАН")
		connection = paint(ansiYellow, "пункт 3 — подключить заново")
	case !s.Connected && s.LastError != "":
		state = paint(ansiRed, "ОШИБКА СВЯЗИ")
		connection = paint(ansiRed, s.LastError)
	case !s.Connected:
		state = paint(ansiYellow, "ПОДКЛЮЧАЕТСЯ")
		connection = paint(ansiMuted, "ожидаю ответ")
	}
	lines := []frameLine{{"  " + paint(ansiMuted, "зонд") + " " + state + paint(ansiMuted, " · ") + connection, 0}}
	if o.IP != "" {
		network := o.IP
		if o.Country != "" {
			network += ", " + o.Country
		}
		if o.ASN > 0 {
			network += fmt.Sprintf(", AS%d", o.ASN)
		}
		lines = append(lines, frameLine{"  " + paint(ansiMuted, "сеть") + " " + network, 0})
	}
	if o.VPN {
		text := paint(ansiRed, "VPN перехватывает трафик")
		if o.Direct {
			text = paint(ansiYellow, "исходная сеть")
			if o.Interface != "" {
				text += paint(ansiMuted, ", "+o.Interface)
			}
		}
		lines = append(lines, frameLine{"  " + paint(ansiMuted, "маршрут") + " " + text, 1})
	} else if o.Interface != "" {
		lines = append(lines, frameLine{"  " + paint(ansiMuted, "маршрут") + " " + paint(ansiGreen, "прямое подключение") + paint(ansiMuted, ", "+o.Interface), 2})
	}
	return lines
}

func offStatusFrame(backgroundRunning bool) []frameLine {
	state := paint(ansiMuted, "ВЫКЛЮЧЕН")
	note := "Данные не отправляются, пока зонд выключен."
	if backgroundRunning {
		state = paint(ansiGreen, "РАБОТАЕТ В ФОНЕ")
		note = "Данные отправляет фоновая служба — это окно ничего не отправляет."
	}
	return []frameLine{
		{"  " + label("зонд") + state, 0},
		{"  " + paint(ansiMuted, note), 1},
	}
}

func fullMenuFrame(probeEnabled, serviceSupported bool) []string {
	lines := []string{"  " + paint(ansiMuted, "Действия")}
	if probeEnabled {
		lines = append(lines, "  "+paint(ansiMagenta, "1")+"  Выключить зонд")
	} else {
		lines = append(lines, "  "+paint(ansiMagenta, "1")+"  Включить зонд")
	}
	lines = append(lines,
		"  "+paint(ansiMagenta, "2")+"  Обновить статус",
		"  "+paint(ansiMagenta, "3")+"  Сменить имя зонда",
		"  "+paint(ansiMagenta, "4")+"  Сменить адрес статус-страницы",
	)
	if serviceSupported {
		lines = append(lines, "  "+paint(ansiMagenta, "5")+"  Фоновая служба и автозапуск")
	}
	if probeEnabled {
		lines = append(lines, "  "+paint(ansiMagenta, "0")+"  Выключить и выйти")
	} else {
		lines = append(lines, "  "+paint(ansiMagenta, "0")+"  Выход")
	}
	lines = append(lines, paint(ansiMuted, "  Ctrl+C — выключить и выйти"), "", promptLine())
	return lines
}

func compactMenuFrame(probeEnabled, serviceSupported bool) []string {
	toggle := "Включить"
	if probeEnabled {
		toggle = "Выключить"
	}
	first := []string{
		paint(ansiMagenta, "1") + " " + toggle,
		paint(ansiMagenta, "2") + " Обновить",
		paint(ansiMagenta, "3") + " Имя",
	}
	second := []string{paint(ansiMagenta, "4") + " Адрес"}
	if serviceSupported {
		second = append(second, paint(ansiMagenta, "5")+" Служба")
	}
	second = append(second, paint(ansiMagenta, "0")+" Выход")
	lines := []string{
		"  " + paint(ansiMuted, "Действия"),
		"  " + strings.Join(first, "  ·  "),
		"  " + strings.Join(second, "  ·  "),
		"",
		promptLine(),
	}
	return lines
}
