// Package probe is linked only into the external agent, never into the backend.
package probe

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"meowzond/internal/model"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Control struct {
	URL    string `json:"url"`
	Body   string `json:"body"`
	Status int    `json:"status"`
}
type Config struct {
	Server            string    `json:"server"`
	ProbeID           string    `json:"probe_id"`
	ProbeName         string    `json:"probe_name,omitempty"`
	Token             string    `json:"token"`
	Development       bool      `json:"development"`
	Controls          []Control `json:"controls"`
	IdentityURL       string    `json:"identity_url,omitempty"`
	IdentityURLs      []string  `json:"identity_urls,omitempty"`
	ExpectedInterface string    `json:"expected_interface,omitempty"`
	XrayPath          string    `json:"xray_path"`
	XraySHA256        string    `json:"xray_sha256"`
	Spool             string    `json:"spool"`
}

type RuntimeStatus struct {
	Connected   bool              `json:"connected"`
	LastContact int64             `json:"last_contact"`
	LastError   string            `json:"last_error,omitempty"`
	Observation model.Observation `json:"observation"`
	Pending     int               `json:"pending"`
}

type Runner struct {
	Config   Config
	client   *http.Client
	xray     *xrayProcess
	pending  []model.Result
	roots    *x509.CertPool // Optional trust roots for controlled local acceptance tests.
	health   model.Observation
	healthAt time.Time
	statusMu sync.RWMutex
	status   RuntimeStatus
	path     networkPath
	log      io.Writer
}

func DefaultControls() []Control {
	return []Control{
		{URL: "https://www.google.com/generate_204", Status: http.StatusNoContent},
		{URL: "https://www.msftconnecttest.com/connecttest.txt", Body: "Microsoft Connect Test", Status: http.StatusOK},
	}
}

func DefaultIdentityURLs() []string {
	return []string{"https://ipwho.is/", "https://ipinfo.io/json"}
}

func DevelopmentServer(raw string) bool {
	u, e := url.Parse(strings.TrimSpace(raw))
	return e == nil && u.Scheme == "http"
}

func directClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, MaxIdleConns: 4, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func Load(path string) (Config, error) {
	var c Config
	b, e := os.ReadFile(path)
	if e != nil {
		return c, e
	}
	e = json.Unmarshal(b, &c)
	if e != nil {
		return c, fmt.Errorf("invalid probe config JSON")
	}
	if c.Spool == "" {
		c.Spool = "probe-spool.json"
	}
	if len(c.Controls) == 0 {
		c.Controls = DefaultControls()
	}
	if len(c.IdentityURLs) == 0 && c.IdentityURL == "" {
		c.IdentityURLs = DefaultIdentityURLs()
	}
	return c, e
}

func Save(path string, c Config) error {
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	dir := filepath.Dir(path)
	if dir != "." {
		if e = os.MkdirAll(dir, 0700); e != nil {
			return e
		}
	}
	tmp := path + ".tmp"
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, path)
}

func Register(ctx context.Context, server, name string) (Config, error) {
	var c Config
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return c, fmt.Errorf("укажите имя зонда")
	}
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	dev := DevelopmentServer(server)
	if !serverURLAllowed(server, dev) {
		return c, fmt.Errorf("в программе указан некорректный адрес статус-страницы")
	}
	u, e := url.Parse(server)
	if e != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return c, fmt.Errorf("в программе указан некорректный адрес статус-страницы")
	}
	body, _ := json.Marshal(map[string]string{"name": name, "platform": runtime.GOOS + "/" + runtime.GOARCH})
	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(reqCtx, "POST", server+"/api/probe/register", bytes.NewReader(body))
	if e != nil {
		return c, e
	}
	req.Header.Set("Content-Type", "application/json")
	client := directClient(20 * time.Second)
	defer client.CloseIdleConnections()
	resp, e := client.Do(req)
	if e != nil {
		return c, fmt.Errorf("не удалось связаться со статус-страницей: проверьте интернет")
	}
	defer resp.Body.Close()
	var out struct {
		ProbeID   string `json:"probe_id"`
		ProbeName string `json:"probe_name"`
		Token     string `json:"token"`
		Error     string `json:"error"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&out) != nil {
		return c, fmt.Errorf("сервер вернул неожиданный ответ (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		if out.Error == "" {
			out.Error = fmt.Sprintf("сервер отклонил подключение (HTTP %d)", resp.StatusCode)
		}
		return c, fmt.Errorf("%s", out.Error)
	}
	if out.ProbeID == "" || len(out.Token) < 32 {
		return c, fmt.Errorf("сервер не выдал доступ для зонда")
	}
	return Config{
		Server:       server,
		ProbeID:      out.ProbeID,
		ProbeName:    out.ProbeName,
		Token:        out.Token,
		Development:  dev,
		Controls:     DefaultControls(),
		IdentityURLs: DefaultIdentityURLs(),
	}, nil
}

func serverURLAllowed(raw string, dev bool) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	return dev && u.Scheme == "http"
}

func New(c Config) (*Runner, error) {
	if !serverURLAllowed(c.Server, c.Development) || c.ProbeID == "" || len(c.Token) < 32 || len(c.Controls) == 0 || len(c.Controls) > 4 {
		return nil, fmt.Errorf("configure HTTPS server, probe credentials and 1–4 independent controls")
	}
	for _, ctrl := range c.Controls {
		if !allowedURL(ctrl.URL, c.Development) || (ctrl.Body == "" && ctrl.Status != http.StatusNoContent) || len(ctrl.Body) > 4096 || ctrl.Status < 100 || ctrl.Status > 599 {
			return nil, fmt.Errorf("control must have HTTPS URL and expected body")
		}
	}
	if c.IdentityURL != "" && !allowedURL(c.IdentityURL, c.Development) {
		return nil, fmt.Errorf("identity URL must use HTTPS")
	}
	if len(c.IdentityURLs) > 4 {
		return nil, fmt.Errorf("too many identity endpoints")
	}
	for _, identity := range c.IdentityURLs {
		if !allowedURL(identity, c.Development) {
			return nil, fmt.Errorf("identity URL must use HTTPS")
		}
	}
	r := &Runner{Config: c, client: directClient(15 * time.Second)}
	b, e := os.ReadFile(c.Spool)
	if e == nil {
		if json.Unmarshal(b, &r.pending) != nil {
			return nil, fmt.Errorf("invalid spool; preserve it for investigation")
		}
		if len(r.pending) > 50 {
			return nil, fmt.Errorf("spool too large")
		}
	}
	return r, nil
}
func allowedURL(raw string, dev bool) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return dev && u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback()))
}
func (r *Runner) api(ctx context.Context, path string, in, out any) error {
	b, e := json.Marshal(in)
	if e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(r.Config.Server, "/")+path, bytes.NewReader(b))
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+r.Config.ProbeID+"."+r.Config.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, e := r.client.Do(req)
	if e != nil {
		return fmt.Errorf("backend unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("backend HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}
func (r *Runner) persist() error {
	b, e := json.Marshal(r.pending)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(r.Config.Spool), 0700); e != nil {
		return e
	}
	tmp := r.Config.Spool + ".tmp"
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, r.Config.Spool)
}
func (r *Runner) Run(ctx context.Context) error {
	return r.RunWithLog(ctx, os.Stderr)
}

func (r *Runner) RunWithLog(ctx context.Context, w io.Writer) error {
	r.log = w
	defer func() { r.log = nil }()
	defer r.Close()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if e := r.Step(ctx); e != nil && ctx.Err() == nil {
			r.logf("error: %v", e)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (r *Runner) logf(format string, args ...any) {
	if r.log == nil {
		return
	}
	values := append([]any{time.Now().Format("15:04:05")}, args...)
	fmt.Fprintf(r.log, "%s "+format+"\n", values...)
}

func logText(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(value)
}

func taskTarget(m model.Monitor) string {
	switch m.Kind {
	case "http":
		u, e := url.Parse(m.URL)
		if e != nil {
			return "invalid-url"
		}
		u.User = nil
		u.RawQuery = ""
		u.Fragment = ""
		return u.String()
	case "dns":
		record := strings.TrimSpace(m.DNSRecord + " " + m.Address)
		if record == "" {
			return "?"
		}
		return record
	default:
		if m.Address == "" {
			return "?"
		}
		if m.Port > 0 {
			return net.JoinHostPort(m.Address, strconv.Itoa(m.Port))
		}
		return m.Address
	}
}
func (r *Runner) Step(ctx context.Context) error {
	if e := r.step(ctx); e != nil {
		r.statusMu.Lock()
		r.status.Connected = false
		r.status.LastError = e.Error()
		r.status.Pending = len(r.pending)
		r.statusMu.Unlock()
		return e
	}
	return nil
}

func (r *Runner) step(ctx context.Context) error {
	if r.xray != nil && (time.Now().Unix() > r.xray.expires || time.Since(r.xray.used) > 2*time.Minute) {
		r.xray.stop()
		r.xray = nil
	}
	if len(r.pending) > 0 {
		var ack struct {
			Accepted []string `json:"accepted"`
		}
		if e := r.api(ctx, "/api/probe/results", r.pending, &ack); e != nil {
			return e
		}
		accepted := map[string]bool{}
		for _, id := range ack.Accepted {
			accepted[id] = true
		}
		remaining := []model.Result{}
		for _, v := range r.pending {
			if !accepted[v.TaskID] {
				remaining = append(remaining, v)
			}
		}
		r.pending = remaining
		if e := r.persist(); e != nil {
			return fmt.Errorf("cannot persist result spool")
		}
	}
	o := r.Health(ctx)
	var poll struct {
		Tasks []model.Task `json:"tasks"`
	}
	if e := r.api(ctx, "/api/probe/poll", o, &poll); e != nil {
		return e
	}
	r.statusMu.Lock()
	r.status = RuntimeStatus{Connected: true, LastContact: time.Now().Unix(), Observation: o, Pending: len(r.pending)}
	r.statusMu.Unlock()
	for _, t := range poll.Tasks {
		if len(r.pending) >= 50 {
			return fmt.Errorf("result spool full")
		}
		target := taskTarget(t.Monitor)
		v := r.Check(ctx, t, o)
		outcome := v.Outcome
		switch outcome {
		case "up":
			outcome = "OK"
		case "down":
			outcome = "FAIL"
		case "unknown":
			outcome = "UNKNOWN"
		case "configuration_error":
			outcome = "CONFIG"
		}
		stage := ""
		if v.Stage != "" {
			stage = " (" + v.Stage + ")"
		}
		r.logf("check %s · %s · %s → %s%s · %.0f ms", logText(t.Monitor.Name), t.Monitor.Kind, target, outcome, stage, v.Latency)
		r.pending = append(r.pending, v)
		if e := r.persist(); e != nil {
			return fmt.Errorf("cannot persist result spool")
		}
	}
	r.statusMu.Lock()
	r.status.Pending = len(r.pending)
	r.statusMu.Unlock()
	return nil
}

func (r *Runner) Status() RuntimeStatus {
	r.statusMu.RLock()
	defer r.statusMu.RUnlock()
	return r.status
}
func (r *Runner) Close() {
	if r.xray != nil {
		r.xray.stop()
		r.xray = nil
	}
	r.client.CloseIdleConnections()
}
func (r *Runner) Health(ctx context.Context) model.Observation {
	if !r.healthAt.IsZero() && time.Since(r.healthAt) < 20*time.Second {
		return r.health
	}
	o := model.Observation{Platform: runtime.GOOS + "/" + runtime.GOARCH}
	defer func() { r.health = o; r.healthAt = time.Now() }()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	path := detectNetworkPath(ctx, r.Config.ExpectedInterface)
	o.VPN = path.VPN
	o.Interface = path.Interface
	baseClient := directClient(4 * time.Second)
	defer baseClient.CloseIdleConnections()
	client := baseClient
	bound := false
	if path.VPN && path.Available && path.Interface != "" {
		client = r.boundClient(path, 4*time.Second)
		defer client.CloseIdleConnections()
		bound = true
	}
	healthy := r.controlsHealthy(ctx, client)
	boundOK := bound && healthy
	if bound && !healthy {
		// Internet through the VPN may be fine while binding to the original
		// interface is not permitted. Keep the heartbeat, but never claim the path.
		healthy = r.controlsHealthy(ctx, baseClient)
	}
	if !path.VPN {
		path.Enforced = true
	} else if boundOK {
		path.Enforced = true
	} else if path.Available {
		path.Reason = "не удалось привязать исходящий трафик к исходному интерфейсу"
	}
	r.path = path
	o.Healthy = healthy
	o.PathError = r.pathError(&path)
	if !healthy || (path.VPN && !path.Enforced) {
		return o
	}
	if identity, ok := r.readIdentity(ctx, client); ok {
		o.IP = identity.IP
		o.ASN = identity.ASN
		o.Country = identity.Country
		o.City = identity.City
		o.Operator = identity.Operator
		o.Access = identity.Access
	}
	o.Direct = path.Available && path.Enforced && net.ParseIP(o.IP) != nil && o.ASN > 0 && len(o.Country) == 2
	return o
}

func (r *Runner) controlsHealthy(ctx context.Context, client *http.Client) bool {
	for _, c := range r.Config.Controls {
		req, e := http.NewRequestWithContext(ctx, "GET", c.URL, nil)
		if e != nil {
			continue
		}
		resp, e := client.Do(req)
		if e != nil {
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4097))
		resp.Body.Close()
		matches := strings.Contains(string(b), c.Body)
		if c.Status == http.StatusNoContent {
			matches = len(b) == 0
		}
		if resp.StatusCode == c.Status && len(b) <= 4096 && matches {
			return true
		}
	}
	return false
}

func (r *Runner) boundClient(path networkPath, timeout time.Duration) *http.Client {
	dialer := path.dialer(5 * time.Second)
	return &http.Client{Timeout: timeout, Transport: &http.Transport{Proxy: nil, DialContext: dialer.DialContext, MaxIdleConns: 4, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

var tunnelNames = regexp.MustCompile(`(?i)(wireguard|tailscale|zerotier|openvpn|wintun|sing-box|clash|v2ray|vpn|\btun\d*\b|\btap\d*\b|\bwg\d*\b|ppp)`)
var orgASN = regexp.MustCompile(`(?i)^AS(\d+)`)
var mobileOperator = regexp.MustCompile(`(?i)(mobile|cellular|lte|5g|gsm|beeline|megafon|tele2|yota|билайн|мегафон|мтс)`)
var hostingOperator = regexp.MustCompile(`(?i)(hosting|cloud|datacenter|data center|colo|vps|vds|server|ovh|hetzner|linode|digitalocean|amazon|google|microsoft|selectel|timeweb|aeza|firstvds|vultr|contabo|leaseweb)`)

func inferAccess(operator string) string {
	switch {
	case mobileOperator.MatchString(operator):
		return "mobile"
	case hostingOperator.MatchString(operator):
		return "datacenter"
	default:
		return "unknown"
	}
}

func (r *Runner) readIdentity(ctx context.Context, client *http.Client) (model.Observation, bool) {
	urls := append([]string(nil), r.Config.IdentityURLs...)
	if len(urls) == 0 && r.Config.IdentityURL != "" {
		urls = []string{r.Config.IdentityURL}
	}
	for _, endpoint := range urls {
		req, e := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		if e != nil {
			continue
		}
		resp, e := client.Do(req)
		if e != nil {
			continue
		}
		var v struct {
			IP         string `json:"ip"`
			ASN        int    `json:"asn"`
			Country    string `json:"country"`
			Code       string `json:"country_code"`
			City       string `json:"city"`
			Org        string `json:"org"`
			Success    *bool  `json:"success"`
			Connection struct {
				ASN int    `json:"asn"`
				Org string `json:"org"`
				ISP string `json:"isp"`
			} `json:"connection"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&v)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || decodeErr != nil || (v.Success != nil && !*v.Success) {
			continue
		}
		country := strings.ToUpper(strings.TrimSpace(v.Code))
		if len(country) != 2 {
			country = strings.ToUpper(strings.TrimSpace(v.Country))
		}
		asn := v.ASN
		if asn == 0 {
			asn = v.Connection.ASN
		}
		operator := strings.TrimSpace(v.Connection.Org)
		if operator == "" {
			operator = strings.TrimSpace(v.Connection.ISP)
		}
		if operator == "" {
			operator = strings.TrimSpace(v.Org)
		}
		if asn == 0 {
			if match := orgASN.FindStringSubmatch(operator); len(match) == 2 {
				asn, _ = strconv.Atoi(match[1])
			}
		}
		if net.ParseIP(strings.TrimSpace(v.IP)) == nil || asn == 0 || len(country) != 2 {
			continue
		}
		return model.Observation{IP: strings.TrimSpace(v.IP), ASN: asn, Country: country, City: strings.TrimSpace(v.City), Operator: operator, Access: inferAccess(operator)}, true
	}
	return model.Observation{}, false
}
func (r *Runner) Check(parent context.Context, t model.Task, o model.Observation) model.Result {
	now := time.Now()
	// Reported latency is the network round trip of the check itself. Timing starts
	// after DNS: a slow resolver, a throttled container or the probe's own bookkeeping
	// would otherwise be added to every number the panel shows.
	netStart := now
	m := t.Monitor
	v := model.Result{TaskID: t.ID, MonitorID: m.ID, ProbeID: t.ProbeID, Revision: m.Revision, Timestamp: now.Unix(), Outcome: "unknown", Network: "unknown", Web: "unknown", VPN: "unknown", Observation: o}
	ctx, cancel := context.WithTimeout(parent, time.Duration(m.Timeout)*time.Second)
	defer cancel()
	latencySet := false
	finish := func(outcome, stage string) model.Result {
		v.Outcome = outcome
		v.Stage = stage
		if !latencySet {
			v.Latency = float64(time.Since(netStart).Microseconds()) / 1000
		}
		return v
	}
	if t.Expires < now.Unix() || m.Timeout < 2 || m.Timeout > 30 {
		return finish("configuration_error", "configuration_error")
	}
	if !o.Healthy || (r.path.VPN && !r.path.Enforced) {
		return finish("unknown", "probe_control")
	}
	address := m.Address
	if m.Kind == "http" {
		u, e := url.Parse(m.URL)
		if e != nil {
			return finish("configuration_error", "configuration_error")
		}
		address = u.Hostname()
	}
	if m.Kind == "dns" {
		record := strings.ToUpper(strings.TrimSpace(m.DNSRecord))
		if record == "" {
			record = "A"
		}
		resolver := net.DefaultResolver
		if r.path.Bind && r.path.Enforced {
			dialer := r.path.dialer(time.Duration(m.Timeout) * time.Second)
			resolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, address)
			}}
		}
		var values []string
		var lookupErr error
		switch record {
		case "CNAME":
			var cname string
			cname, lookupErr = resolver.LookupCNAME(ctx, address)
			if lookupErr == nil {
				values = []string{strings.TrimSuffix(cname, ".")}
			}
		case "AAAA":
			var found []net.IP
			found, lookupErr = resolver.LookupIP(ctx, "ip6", address)
			for _, ip := range found {
				values = append(values, ip.String())
			}
		default:
			var found []net.IP
			found, lookupErr = resolver.LookupIP(ctx, "ip4", address)
			for _, ip := range found {
				values = append(values, ip.String())
			}
		}
		if lookupErr != nil {
			v.Errno = errorCode(lookupErr)
			return finish("down", "dns")
		}
		if len(values) == 0 {
			return finish("down", "dns")
		}
		v.DNS = values
		if expected := strings.TrimSpace(m.ExpectedDNS); expected != "" && !dnsMatch(record, values, expected) {
			return finish("down", "unexpected_response")
		}
		return finish("up", "")
	}
	ips, e := net.DefaultResolver.LookupHost(ctx, address)
	if e != nil {
		v.Errno = errorCode(e)
		return finish("down", "dns")
	}
	netStart = time.Now()
	v.DNS = ips
	if m.Kind == "icmp" {
		state, ms, configurationError := r.ping(ctx, address)
		if configurationError {
			return finish("configuration_error", "configuration_error")
		}
		if ms > 0 {
			v.Latency, latencySet = ms, true
		}
		v.Network = state
		v.ICMP = state
		if state == "up" {
			return finish("up", "")
		}
		return finish("down", "timeout")
	}
	if m.Kind == "tcp" || m.Kind == "vpn" {
		var icmp chan string
		if m.Kind == "tcp" {
			// TCP loss can be a TSPU block rather than a dead server: check ICMP
			// in parallel so the result always distinguishes the two.
			icmp = make(chan string, 1)
			go func() {
				pingCtx, cancelPing := context.WithTimeout(parent, 3*time.Second)
				state, _, _ := r.ping(pingCtx, address)
				cancelPing()
				icmp <- state
			}()
		}
		dialStart := time.Now()
		dialer := r.path.dialer(time.Duration(m.Timeout) * time.Second)
		conn, e := dialer.DialContext(ctx, "tcp", net.JoinHostPort(address, fmt.Sprint(m.Port)))
		if m.Kind == "tcp" {
			v.Latency = float64(time.Since(dialStart).Microseconds()) / 1000
			latencySet = true
			v.ICMP = <-icmp
		}
		if e != nil {
			v.Network = "down"
			v.Errno = errorCode(e)
			if m.Kind == "vpn" {
				// A failed TCP connect is not proof that the server is down.
				pingCtx, cancelPing := context.WithTimeout(parent, 3*time.Second)
				v.ICMP, _, _ = r.ping(pingCtx, address)
				cancelPing()
				v.VPN = "down"
			}
			return finish("down", "tcp")
		}
		conn.Close()
		v.Network = "up"
		if m.Kind == "tcp" {
			return finish("up", "")
		}
	}
	if m.Kind == "tls" {
		dialer := r.path.dialer(time.Duration(m.Timeout) * time.Second)
		conn, e := dialer.DialContext(ctx, "tcp", net.JoinHostPort(address, fmt.Sprint(m.Port)))
		if e != nil {
			v.Network = "down"
			v.Errno = errorCode(e)
			return finish("down", "tcp")
		}
		v.Network = "up"
		serverName := strings.TrimSpace(m.ServerName)
		if serverName == "" {
			serverName = address
		}
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName: serverName,
			RootCAs:    r.roots,
			MinVersion: tls.VersionTLS12,
		})
		if e = tlsConn.HandshakeContext(ctx); e != nil {
			conn.Close()
			v.Errno = errorCode(e)
			if tlsVerificationError(e) {
				return finish("down", "tls_verify")
			}
			return finish("down", "tls_handshake")
		}
		state := tlsConn.ConnectionState()
		if len(state.VerifiedChains) > 0 && len(state.VerifiedChains[0]) > 0 {
			v.TLSExpires = state.VerifiedChains[0][0].NotAfter.Unix()
		} else if len(state.PeerCertificates) > 0 {
			v.TLSExpires = state.PeerCertificates[0].NotAfter.Unix()
		}
		tlsConn.Close()
		return finish("up", "")
	}
	if m.Kind == "http" {
		if !allowedURL(m.URL, r.Config.Development) {
			return finish("configuration_error", "configuration_error")
		}
		client := r.boundClient(r.path, time.Duration(m.Timeout)*time.Second)
		if !r.path.Bind || !r.path.Enforced {
			client = directClient(time.Duration(m.Timeout) * time.Second)
		}
		defer client.CloseIdleConnections()
		req, e := http.NewRequestWithContext(ctx, "GET", m.URL, nil)
		if e != nil {
			return finish("configuration_error", "configuration_error")
		}
		resp, e := client.Do(req)
		if e != nil {
			v.Web = "down"
			v.Errno = errorCode(e)
			return finish("down", "proxy_request")
		}
		defer resp.Body.Close()
		b, e := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if e != nil || resp.StatusCode != m.ExpectedStatus || !strings.Contains(string(b), m.ExpectedBody) {
			v.Web = "down"
			return finish("down", "unexpected_response")
		}
		v.Web = "up"
		return finish("up", "")
	}
	if m.Kind == "vpn" {
		if m.ProfileExpires <= now.Unix() || len(m.Outbound) == 0 {
			return finish("configuration_error", "configuration_error")
		}
		if !o.Direct && !r.Config.Development {
			return finish("unknown", "probe_control")
		}
		outcome, stage, errno := r.vpn(ctx, m)
		v.Errno = errno
		v.VPN = outcome
		return finish(outcome, stage)
	}
	return finish("configuration_error", "configuration_error")
}

// pingRTT matches the reply line of iputils, busybox and Windows ping, which all
// report the round trip as "time=12.3 ms" (Windows writes "time<1ms" for a sub
// millisecond reply).
var pingRTT = regexp.MustCompile(`(?i)time[=<]\s*([0-9]+(?:[.,][0-9]+)?)\s*ms`)

// ping reports reachability and the measured round trip in milliseconds; the
// latency is 0 when the reply line could not be parsed, so the caller falls back
// to its own stopwatch.
func (r *Runner) ping(ctx context.Context, address string) (string, float64, bool) {
	if strings.HasPrefix(address, "-") {
		return "unknown", 0, true
	}
	args := []string{"-n", "-c", "1", "-W", "2", address}
	if runtime.GOOS == "windows" {
		args = []string{"-n", "1", "-w", "2000", address}
	}
	if r.path.VPN && r.path.Enforced {
		if runtime.GOOS == "windows" {
			if r.path.LocalIP == nil || r.path.LocalIP.To4() == nil {
				return "unknown", 0, true
			}
			args = append(args, "-S", r.path.LocalIP.String())
		} else {
			args = append(args, "-I", r.path.Interface)
		}
	}
	out, e := exec.CommandContext(ctx, "ping", args...).CombinedOutput()
	if e != nil {
		var commandError *exec.Error
		if errors.As(e, &commandError) || os.IsPermission(e) {
			return "unknown", 0, true
		}
		if strings.Contains(strings.ToLower(string(out)), "permi") || strings.Contains(strings.ToLower(string(out)), "privilege") {
			return "unknown", 0, true
		}
		return "down", 0, false
	}
	text := string(out)
	if runtime.GOOS == "windows" && !strings.Contains(strings.ToLower(text), "ttl=") {
		return "down", 0, false
	}
	ms := parsePingRTT(text)
	return "up", ms, false
}

// parsePingRTT reads the round trip from the last reply line; 0 means the output
// was not in a shape we recognise and the caller should time the command itself.
func parsePingRTT(text string) float64 {
	matches := pingRTT.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return 0
	}
	value := strings.ReplaceAll(matches[len(matches)-1][1], ",", ".")
	ms, e := strconv.ParseFloat(value, 64)
	if e != nil || ms <= 0 || ms > 60_000 {
		return 0
	}
	return ms
}

func dnsMatch(record string, values []string, expected string) bool {
	expected = strings.TrimSuffix(strings.TrimSpace(expected), ".")
	for _, value := range values {
		value = strings.TrimSuffix(strings.TrimSpace(value), ".")
		if strings.EqualFold(value, expected) {
			return true
		}
		if record == "A" || record == "AAAA" {
			if ip := net.ParseIP(value); ip != nil {
				if want := net.ParseIP(expected); want != nil && ip.Equal(want) {
					return true
				}
			}
		}
	}
	return false
}

func tlsVerificationError(err error) bool {
	var verification *tls.CertificateVerificationError
	if errors.As(err, &verification) {
		return true
	}
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	var unknown x509.UnknownAuthorityError
	return errors.As(err, &invalid) || errors.As(err, &hostname) || errors.As(err, &unknown)
}

func errorCode(e error) string {
	if e == nil {
		return ""
	}
	if n, ok := e.(net.Error); ok && n.Timeout() {
		return "timeout"
	}
	s := strings.ToLower(e.Error())
	for _, v := range []string{"refused", "reset", "unreachable", "no such host"} {
		if strings.Contains(s, v) {
			return strings.ReplaceAll(v, " ", "_")
		}
	}
	return "network_error"
}
func verifyBinary(path, want string) error {
	if len(want) != 64 {
		return fmt.Errorf("pin Xray SHA256")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return fmt.Errorf("Xray binary unavailable")
	}
	sum := sha256.Sum256(b)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), want) {
		return fmt.Errorf("Xray checksum mismatch")
	}
	return nil
}

type xrayProcess struct {
	key, dir string
	cmd      *exec.Cmd
	addr     string
	done     chan struct{}
	once     sync.Once
	expires  int64
	used     time.Time
	release  func()
}

func (p *xrayProcess) stop() {
	p.once.Do(func() {
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		<-p.done
		if p.release != nil {
			p.release()
		}
		_ = os.RemoveAll(p.dir)
	})
}
