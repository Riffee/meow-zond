package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
)

type routeEntry struct {
	Interface string
	Index     int
	Metric    int
	Family    int
	Physical  bool
	Tunnel    bool
}

type networkPath struct {
	Interface string
	Index     int
	LocalIP   net.IP
	VPN       bool
	Available bool
	Enforced  bool
	Bind      bool
	Reason    string
}

func detectNetworkPath(ctx context.Context, expected string) networkPath {
	routes, routeErr := defaultRoutes(ctx)
	activeTunnel := ""
	if interfaces, err := net.Interfaces(); err == nil {
		for _, iface := range interfaces {
			if iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagLoopback == 0 && tunnelNames.MatchString(iface.Name) {
				activeTunnel = iface.Name
				break
			}
		}
	}
	path := selectNetworkPath(routes, activeTunnel, expected, runtime.GOOS)
	if routeErr != nil && !path.Available && path.Reason == "" {
		path.Reason = "не удалось определить default route"
	}
	if !path.Available {
		return path
	}
	iface, err := interfaceByNameOrIndex(path.Interface, path.Index)
	if err != nil {
		path.Available = false
		path.Reason = "исходный сетевой интерфейс недоступен"
		return path
	}
	path.Interface = iface.Name
	path.Index = iface.Index
	path.LocalIP = interfaceIP(iface)
	if path.VPN && runtime.GOOS == "windows" && path.Index == 0 {
		path.Available = false
		path.Reason = "не удалось получить индекс исходного интерфейса"
	}
	return path
}

func selectNetworkPath(routes []routeEntry, activeTunnel, expected, goos string) networkPath {
	expected = strings.TrimSpace(expected)
	path := networkPath{}
	for i := range routes {
		if tunnelNames.MatchString(routes[i].Interface) {
			routes[i].Tunnel = true
			path.VPN = true
		}
		if goos == "windows" && !routes[i].Physical {
			path.VPN = true
		}
	}
	if activeTunnel != "" {
		path.VPN = true
	}
	if expected != "" {
		filtered := routes[:0]
		for _, route := range routes {
			if strings.EqualFold(route.Interface, expected) {
				filtered = append(filtered, route)
			}
		}
		routes = filtered
		if len(routes) == 0 {
			path.Reason = "указанный интерфейс не является default route"
			return path
		}
	}
	candidates := make([]routeEntry, 0, len(routes))
	for _, route := range routes {
		if route.Tunnel {
			continue
		}
		if goos == "windows" && route.Physical {
			candidates = append(candidates, route)
			continue
		}
		if goos != "windows" {
			candidates = append(candidates, route)
		}
	}
	if len(candidates) == 0 && !path.VPN {
		for _, route := range routes {
			if !route.Tunnel {
				candidates = append(candidates, route)
			}
		}
	}
	if len(candidates) == 0 {
		if path.VPN {
			path.Reason = "VPN перехватывает весь трафик: исходный маршрут не найден"
		} else {
			path.Reason = "физический default route не найден"
		}
		return path
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Family != candidates[j].Family {
			return candidates[i].Family == 4
		}
		if candidates[i].Metric != candidates[j].Metric {
			return candidates[i].Metric < candidates[j].Metric
		}
		return candidates[i].Interface < candidates[j].Interface
	})
	chosen := candidates[0]
	path.Interface = chosen.Interface
	path.Index = chosen.Index
	path.Available = true
	path.Bind = path.VPN
	return path
}
func (r *Runner) pathError(path *networkPath) string {
	if path.Reason != "" {
		return path.Reason
	}
	if path.VPN && !path.Available {
		return "VPN перехватывает весь трафик: исходный default route не найден"
	}
	if path.VPN && !path.Enforced {
		return "не удалось направить проверки через исходный интерфейс"
	}
	return ""
}

func interfaceByNameOrIndex(name string, index int) (net.Interface, error) {
	if name != "" {
		if iface, err := net.InterfaceByName(name); err == nil {
			return *iface, nil
		}
	}
	if index > 0 {
		if iface, err := net.InterfaceByIndex(index); err == nil {
			return *iface, nil
		}
	}
	return net.Interface{}, fmt.Errorf("interface not found")
}

func interfaceIP(iface net.Interface) net.IP {
	addrs, err := iface.Addrs()
	if err != nil {
		return nil
	}
	var fallback net.IP
	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil {
			continue
		}
		if ip.To4() != nil {
			return ip
		}
		if fallback == nil {
			fallback = ip
		}
	}
	return fallback
}

func defaultRoutes(ctx context.Context) ([]routeEntry, error) {
	if runtime.GOOS == "windows" {
		return windowsDefaultRoutes(ctx)
	}
	return linuxDefaultRoutes(ctx)
}

type windowsRoutePayload struct {
	Routes   []windowsRoute `json:"routes"`
	Physical []string       `json:"physical"`
}

type windowsRoute struct {
	Interface string `json:"interface"`
	Index     int    `json:"index"`
	Metric    int    `json:"metric"`
	Family    int    `json:"family"`
	Physical  bool   `json:"physical"`
}

func windowsDefaultRoutes(ctx context.Context) ([]routeEntry, error) {
	const script = `$ProgressPreference='SilentlyContinue'; $WarningPreference='SilentlyContinue'; [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new(); $phys = @(Get-NetAdapter -Physical -ErrorAction SilentlyContinue | Where-Object { $_.Status -eq 'Up' } | Select-Object -ExpandProperty Name); ` +
		`$routes = @(Get-NetRoute -ErrorAction Stop | Where-Object { $_.DestinationPrefix -eq '0.0.0.0/0' -or $_.DestinationPrefix -eq '::/0' } | ForEach-Object { [pscustomobject]@{ interface=[string]$_.InterfaceAlias; index=[int]$_.InterfaceIndex; metric=[int]$_.RouteMetric; family=$(if ($_.DestinationPrefix -like '*:*') { 6 } else { 4 }); physical=($phys -contains $_.InterfaceAlias) } }); ` +
		`[pscustomobject]@{routes=$routes; physical=$phys} | ConvertTo-Json -Compress -Depth 5`
	out, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		return nil, err
	}
	start, end := bytes.IndexByte(out, '{'), bytes.LastIndexByte(out, '}')
	if start < 0 || end <= start {
		return nil, fmt.Errorf("PowerShell did not return route JSON")
	}
	var payload windowsRoutePayload
	if err := json.Unmarshal(out[start:end+1], &payload); err != nil {
		return nil, err
	}
	physical := map[string]bool{}
	for _, name := range payload.Physical {
		physical[strings.ToLower(name)] = true
	}
	routes := make([]routeEntry, 0, len(payload.Routes))
	for _, route := range payload.Routes {
		if route.Interface == "" {
			continue
		}
		routes = append(routes, routeEntry{
			Interface: route.Interface,
			Index:     route.Index,
			Metric:    route.Metric,
			Family:    route.Family,
			Physical:  route.Physical || physical[strings.ToLower(route.Interface)],
		})
	}
	sort.SliceStable(routes, func(i, j int) bool { return routes[i].Metric < routes[j].Metric })
	return routes, nil
}

func linuxDefaultRoutes(ctx context.Context) ([]routeEntry, error) {
	out, err := exec.CommandContext(ctx, "ip", "-json", "route", "show", "default").Output()
	if err != nil {
		return nil, err
	}
	routes, err := parseIPRoutes(out, 4)
	if err != nil {
		return nil, err
	}
	if out6, err6 := exec.CommandContext(ctx, "ip", "-json", "-6", "route", "show", "default").Output(); err6 == nil {
		if routes6, err6 := parseIPRoutes(out6, 6); err6 == nil {
			routes = append(routes, routes6...)
		}
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("default route not found")
	}
	return routes, nil
}

func parseIPRoutes(data []byte, family int) ([]routeEntry, error) {
	var raw []struct {
		Dev     string `json:"dev"`
		Gateway string `json:"gateway"`
		Metric  int    `json:"metric"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	routes := make([]routeEntry, 0, len(raw))
	for _, route := range raw {
		if route.Dev == "" {
			continue
		}
		routes = append(routes, routeEntry{Interface: route.Dev, Metric: route.Metric, Family: family, Physical: !tunnelNames.MatchString(route.Dev)})
	}
	return routes, nil
}

func (p networkPath) dialer(timeout time.Duration) *net.Dialer {
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	if !p.Bind || p.Interface == "" {
		return dialer
	}
	path := p
	dialer.Control = func(network, address string, conn syscall.RawConn) error {
		var bindErr error
		controlErr := conn.Control(func(fd uintptr) {
			bindErr = bindSocketToInterface(fd, network, address, path)
		})
		if controlErr != nil {
			return controlErr
		}
		return bindErr
	}
	return dialer
}
