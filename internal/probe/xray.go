package probe

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"golang.org/x/net/proxy"
	"io"
	"meowzond/internal/model"
	"meowzond/internal/secure"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (r *Runner) ensureXray(ctx context.Context, m model.Monitor) (string, error) {
	key := secure.Hash(string(m.Outbound))
	if r.xray != nil && r.xray.key == key {
		select {
		case <-r.xray.done:
			r.xray.stop()
			r.xray = nil
		default:
			r.xray.used = time.Now()
			return r.xray.addr, nil
		}
	}
	if r.xray != nil {
		r.xray.stop()
		r.xray = nil
	}
	if e := verifyBinary(r.Config.XrayPath, r.Config.XraySHA256); e != nil {
		return "", e
	}
	var outbound map[string]any
	if json.Unmarshal(m.Outbound, &outbound) != nil || outbound["protocol"] != "vless" {
		return "", fmt.Errorf("invalid outbound")
	}
	outbound["tag"] = "vpn"
	outbound["mux"] = map[string]any{"enabled": false}
	if r.path.VPN && r.path.Enforced && r.path.Interface != "" {
		stream, _ := outbound["streamSettings"].(map[string]any)
		if stream == nil {
			stream = map[string]any{}
		}
		sockopt, _ := stream["sockopt"].(map[string]any)
		if sockopt == nil {
			sockopt = map[string]any{}
		}
		sockopt["interface"] = r.path.Interface
		stream["sockopt"] = sockopt
		outbound["streamSettings"] = stream
	}
	// No freedom/direct outbound is available. Destination DNS remains inside SOCKS/Xray.
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return "", e
	}
	addr := listener.Addr().String()
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	config := map[string]any{"log": map[string]any{"loglevel": "none"}, "inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": "socks", "settings": map[string]any{"auth": "noauth", "udp": false}}}, "outbounds": []any{outbound}, "routing": map[string]any{"domainStrategy": "AsIs", "rules": []any{map[string]any{"type": "field", "network": "tcp,udp", "outboundTag": "vpn"}}}, "policy": map[string]any{"levels": map[string]any{"0": map[string]any{"handshake": 5, "connIdle": 15, "uplinkOnly": 1, "downlinkOnly": 1, "bufferSize": 16}}}}
	dir, e := os.MkdirTemp("", "meow-xray-")
	if e != nil {
		return "", e
	}
	b, _ := json.Marshal(config)
	if e = os.WriteFile(filepath.Join(dir, "config.json"), b, 0600); e != nil {
		os.RemoveAll(dir)
		return "", e
	}
	cmd := exec.Command(r.Config.XrayPath, "run", "-config", filepath.Join(dir, "config.json"))
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Env = append(os.Environ(), "GOMEMLIMIT=64MiB", "GOMAXPROCS=1")
	prepareProcess(cmd)
	if e = cmd.Start(); e != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("cannot start pinned Xray")
	}
	release, e := constrainProcess(cmd)
	if e != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("cannot constrain Xray process")
	}
	p := &xrayProcess{key: key, dir: dir, cmd: cmd, addr: addr, done: make(chan struct{}), expires: m.ProfileExpires, used: time.Now(), release: release}
	go func() { _ = cmd.Wait(); close(p.done) }()
	r.xray = p
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			p.stop()
			r.xray = nil
			return "", ctx.Err()
		case <-p.done:
			p.stop()
			r.xray = nil
			return "", fmt.Errorf("Xray rejected profile")
		case <-deadline.C:
			p.stop()
			r.xray = nil
			return "", fmt.Errorf("Xray startup timeout")
		case <-tick.C:
			c, e := net.DialTimeout("tcp", addr, 100*time.Millisecond)
			if e == nil {
				c.Close()
				return addr, nil
			}
		}
	}
}
func (r *Runner) vpn(ctx context.Context, m model.Monitor) (string, string, string) {
	if !allowedURL(m.URL, r.Config.Development) {
		return "configuration_error", "configuration_error", ""
	}
	addr, e := r.ensureXray(ctx, m)
	if e != nil {
		return "configuration_error", "configuration_error", ""
	}
	dialer, e := proxy.SOCKS5("tcp", addr, nil, &net.Dialer{Timeout: 3 * time.Second})
	if e != nil {
		return "configuration_error", "configuration_error", ""
	}
	cd, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return "configuration_error", "configuration_error", ""
	}
	transport := &http.Transport{Proxy: nil, DialContext: cd.DialContext, DisableKeepAlives: true, TLSHandshakeTimeout: 5 * time.Second, MaxResponseHeaderBytes: 8192, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: r.roots}}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	nonce := secure.ID()
	u, e := url.Parse(m.URL)
	if e != nil {
		return "configuration_error", "configuration_error", ""
	}
	q := u.Query()
	q.Set("nonce", nonce)
	u.RawQuery = q.Encode()
	req, e := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if e != nil {
		return "configuration_error", "configuration_error", ""
	}
	resp, e := client.Do(req)
	if e != nil {
		return "down", "proxy_request", errorCode(e)
	}
	defer resp.Body.Close()
	body, e := io.ReadAll(io.LimitReader(resp.Body, 1025))
	if e != nil {
		return "down", "egress", errorCode(e)
	}
	if resp.StatusCode != 200 || len(body) > 1024 || strings.TrimSpace(string(body)) != "meow-status:"+nonce {
		return "down", "unexpected_response", ""
	}
	return "up", "", ""
}
