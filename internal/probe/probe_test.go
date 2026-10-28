package probe

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"meowzond/internal/model"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHTTPAndControlFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "expected") }))
	defer server.Close()
	c := Config{Server: server.URL, ProbeID: "local", Token: strings.Repeat("t", 64), Development: true, Controls: []Control{{URL: server.URL, Body: "expected", Status: 200}}, Spool: filepath.Join(t.TempDir(), "spool.json")}
	runner, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	defer runner.Close()
	o := runner.Health(context.Background())
	if !o.Healthy || o.Direct {
		t.Fatal("health must not assume path verification", o)
	}
	m := model.Monitor{Kind: "http", URL: server.URL, ExpectedStatus: 200, ExpectedBody: "expected", Timeout: 3}
	task := model.Task{Monitor: m, Expires: time.Now().Add(time.Minute).Unix()}
	v := runner.Check(context.Background(), task, o)
	if v.Outcome != "up" {
		t.Fatal(v)
	}
	task.Monitor.ExpectedBody = "wrong"
	v = runner.Check(context.Background(), task, o)
	if v.Outcome != "down" || v.Stage != "unexpected_response" {
		t.Fatal(v)
	}
	o.Healthy = false
	v = runner.Check(context.Background(), task, o)
	if v.Outcome != "unknown" || v.Stage != "probe_control" {
		t.Fatal(v)
	}
	if allowedURL("http://example.com", true) || allowedURL("http://127.0.0.1", false) {
		t.Fatal("insecure production target allowed")
	}
}
func TestPinnedXrayHTTPSRoundTripAndNoFallback(t *testing.T) {
	t.Run("VLESS", func(t *testing.T) { xrayRoundTrip(t, false) })
	t.Run("VLESS_REALITY", func(t *testing.T) { xrayRoundTrip(t, true) })
}
func xrayRoundTrip(t *testing.T, reality bool) {
	binary := os.Getenv("XRAY_TEST_BINARY")
	sha := os.Getenv("XRAY_TEST_SHA256")
	if binary == "" {
		t.Skip("set XRAY_TEST_BINARY and XRAY_TEST_SHA256 to run official Xray integration acceptance")
	}
	if e := verifyBinary(binary, sha); e != nil {
		t.Fatal(e)
	}
	endpoint := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "meow-status:"+r.URL.Query().Get("nonce")) }))
	defer endpoint.Close()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	id := "00000000-0000-4000-8000-000000000001"
	config := map[string]any{"log": map[string]any{"loglevel": "none"}, "inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": "vless", "settings": map[string]any{"clients": []any{map[string]any{"id": id}}, "decryption": "none"}, "streamSettings": map[string]any{"network": "tcp", "security": "none"}}}, "outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}}}}
	var publicKey string
	if reality {
		key, e := ecdh.X25519().GenerateKey(rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		publicKey = base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
		inbound := config["inbounds"].([]any)[0].(map[string]any)
		inbound["settings"] = map[string]any{"clients": []any{map[string]any{"id": id, "flow": "xtls-rprx-vision"}}, "decryption": "none"}
		inbound["streamSettings"] = map[string]any{"network": "tcp", "security": "reality", "realitySettings": map[string]any{"show": false, "dest": strings.TrimPrefix(endpoint.URL, "https://"), "serverNames": []string{"localhost"}, "privateKey": base64.RawURLEncoding.EncodeToString(key.Bytes()), "shortIds": []string{"ab"}}}
	}
	dir := t.TempDir()
	b, _ := json.Marshal(config)
	file := filepath.Join(dir, "server.json")
	_ = os.WriteFile(file, b, 0600)
	cmd := exec.Command(binary, "run", "-config", file)
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if e == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Xray fixture did not start")
		}
		time.Sleep(30 * time.Millisecond)
	}
	outbound := map[string]any{"protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{"address": "127.0.0.1", "port": port, "users": []any{map[string]any{"id": id, "encryption": "none"}}}}}, "streamSettings": map[string]any{"network": "tcp", "security": "none"}}
	if reality {
		outbound["settings"] = map[string]any{"vnext": []any{map[string]any{"address": "127.0.0.1", "port": port, "users": []any{map[string]any{"id": id, "encryption": "none", "flow": "xtls-rprx-vision"}}}}}
		outbound["streamSettings"] = map[string]any{"network": "tcp", "security": "reality", "realitySettings": map[string]any{"serverName": "localhost", "fingerprint": "chrome", "publicKey": publicKey, "shortId": "ab"}}
	}
	raw, _ := json.Marshal(outbound)
	m := model.Monitor{Kind: "vpn", Address: "127.0.0.1", Port: port, URL: endpoint.URL, Timeout: 8, ProfileExpires: time.Now().Add(time.Hour).Unix(), Outbound: raw}
	runner, e := New(Config{Server: endpoint.URL, ProbeID: "p", Token: strings.Repeat("t", 64), Controls: []Control{{URL: endpoint.URL, Body: "meow-status:", Status: 200}}, XrayPath: binary, XraySHA256: sha, Spool: filepath.Join(dir, "spool.json")})
	if e != nil {
		t.Fatal(e)
	}
	defer runner.Close()
	runner.roots = x509.NewCertPool()
	runner.roots.AddCert(endpoint.Certificate())
	o := model.Observation{Healthy: true, Direct: true}
	task := model.Task{Monitor: m, Expires: time.Now().Add(time.Minute).Unix()}
	v := runner.Check(context.Background(), task, o)
	if v.Outcome != "up" || v.VPN != "up" {
		t.Fatalf("real HTTPS through Xray failed: %+v", v)
	}
	// Stop the client process. The healthy HTTPS endpoint must never be reached directly as fallback.
	runner.xray.stop()
	runner.xray = nil
	runner.Config.XraySHA256 = strings.Repeat("0", 64)
	v = runner.Check(context.Background(), task, o)
	if v.Outcome != "configuration_error" {
		t.Fatalf("missing Xray must fail closed: %+v", v)
	}
	// Invalid test-account lifetime is configuration failure, independent of network connectivity.
	task.Monitor.ProfileExpires = time.Now().Add(-time.Minute).Unix()
	v = runner.Check(context.Background(), task, o)
	if v.Outcome != "configuration_error" {
		t.Fatal(v)
	}
}

func TestTCPFailureRecordsICMPAnswer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX ping stub")
	}
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "ping"), []byte("#!/bin/sh\nexit 0\n"), 0755); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	runner := &Runner{}
	m := model.Monitor{Kind: "tcp", Address: "127.0.0.1", Port: port, Timeout: 2}
	v := runner.Check(context.Background(), model.Task{Monitor: m, Expires: time.Now().Add(time.Minute).Unix()}, model.Observation{Healthy: true, Direct: true})
	if v.Outcome != "down" || v.Stage != "tcp" || v.ICMP != "up" {
		t.Fatalf("failed TCP check must keep the ICMP answer: %+v", v)
	}
}

func TestICMPLatencyComesFromPingReply(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX ping stub")
	}
	dir := t.TempDir()
	stub := "#!/bin/sh\nsleep 0.2\nprintf '64 bytes from 127.0.0.1: icmp_seq=1 ttl=64 time=4.2 ms\\n'\n"
	if e := os.WriteFile(filepath.Join(dir, "ping"), []byte(stub), 0755); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	runner := &Runner{}
	m := model.Monitor{Kind: "icmp", Address: "localhost", Timeout: 5}
	v := runner.Check(context.Background(), model.Task{Monitor: m, Expires: time.Now().Add(time.Minute).Unix()}, model.Observation{Healthy: true, Direct: true})
	if v.Outcome != "up" {
		t.Fatalf("stubbed ping must report up: %+v", v)
	}
	if v.Latency < 4.1 || v.Latency > 4.3 {
		t.Fatalf("latency must be the reply round trip, not the stopwatch: %v", v.Latency)
	}
}

func TestParsePingRTT(t *testing.T) {
	cases := map[string]float64{
		"PING localhost (127.0.0.1) 56(84) bytes of data.\n64 bytes from localhost: icmp_seq=1 ttl=64 time=12.3 ms\n": 12.3,
		"64 bytes from 1.1.1.1: icmp_seq=2 ttl=57 time=0,45 ms\n":                                                     0.45,
		"PING google.com\n reply from google.com: sequence=0 ttl=57 time=8 ms\n":                                      8,
		"Reply from 1.1.1.1: bytes=32 time<1ms TTL=128\n":                                                             1,
		"PING 1.1.1.1\nno reply\n":               0,
		"64 bytes from 1.1.1.1: time=99999 ms\n": 0,
	}
	for output, want := range cases {
		if got := parsePingRTT(output); got != want {
			t.Errorf("parsePingRTT(%q) = %v, want %v", output, got, want)
		}
	}
}

func TestDNSMonitorTypes(t *testing.T) {
	runner := &Runner{}
	observation := model.Observation{Healthy: true, Direct: true}
	check := func(m model.Monitor) model.Result {
		t.Helper()
		m.Kind = "dns"
		m.Address = "localhost"
		m.Timeout = 3
		return runner.Check(context.Background(), model.Task{Monitor: m, Expires: time.Now().Add(time.Minute).Unix()}, observation)
	}
	v := check(model.Monitor{DNSRecord: "A", ExpectedDNS: "127.0.0.1"})
	if v.Outcome != "up" || !slices.Contains(v.DNS, "127.0.0.1") {
		t.Fatalf("A lookup failed: %+v", v)
	}
	v = check(model.Monitor{DNSRecord: "A", ExpectedDNS: "192.0.2.1"})
	if v.Outcome != "down" || v.Stage != "unexpected_response" {
		t.Fatalf("A mismatch must fail: %+v", v)
	}
	v = check(model.Monitor{DNSRecord: "CNAME"})
	if v.Outcome != "up" || len(v.DNS) == 0 {
		t.Fatalf("CNAME lookup failed: %+v", v)
	}
}

func TestTLSMonitorAndCertificateCheck(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	host, portText, e := net.SplitHostPort(strings.TrimPrefix(server.URL, "https://"))
	if e != nil {
		t.Fatal(e)
	}
	port, e := strconv.Atoi(portText)
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	runner := &Runner{roots: roots}
	m := model.Monitor{Kind: "tls", Address: host, Port: port, ServerName: host, Timeout: 3}
	observation := model.Observation{Healthy: true, Direct: true}
	task := model.Task{Monitor: m, Expires: time.Now().Add(time.Minute).Unix()}
	v := runner.Check(context.Background(), task, observation)
	if v.Outcome != "up" || v.Network != "up" || v.TLSExpires <= time.Now().Unix() {
		t.Fatalf("valid TLS should pass with expiry: %+v", v)
	}
	runner.roots = nil
	v = runner.Check(context.Background(), task, observation)
	if v.Outcome != "down" || v.Stage != "tls_verify" {
		t.Fatalf("untrusted TLS must fail verification: %+v", v)
	}
}

// fixedDNS answers A queries with 127.0.0.1 after a delay and counts the A lookups,
// so a check that resolves the target twice cannot hide it.
type fixedDNS struct {
	conn    *net.UDPConn
	delay   time.Duration
	mu      sync.Mutex
	lookups int
}

func (d *fixedDNS) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lookups
}

func (d *fixedDNS) serve() {
	buffer := make([]byte, 512)
	for {
		size, from, e := d.conn.ReadFromUDP(buffer)
		if e != nil {
			return
		}
		packet := append([]byte(nil), buffer[:size]...)
		question, kind := dnsQuestion(packet)
		if question == nil {
			continue
		}
		if kind == 1 {
			d.mu.Lock()
			d.lookups++
			d.mu.Unlock()
		}
		go func() {
			time.Sleep(d.delay)
			_, _ = d.conn.WriteToUDP(dnsAnswer(packet, question, kind), from)
		}()
	}
}

func dnsQuestion(packet []byte) ([]byte, uint16) {
	if len(packet) < 16 {
		return nil, 0
	}
	i := 12
	for i < len(packet) {
		length := int(packet[i])
		if length == 0 {
			i++
			break
		}
		if length&0xc0 != 0 {
			i += 2
			break
		}
		i += 1 + length
	}
	if i+4 > len(packet) {
		return nil, 0
	}
	return packet[12 : i+4], binary.BigEndian.Uint16(packet[i : i+2])
}

func dnsAnswer(packet []byte, question []byte, kind uint16) []byte {
	var answers byte
	if kind == 1 {
		answers = 1
	}
	reply := append([]byte{packet[0], packet[1], 0x81, 0x80, 0x00, 0x01, answers, 0, 0, 0, 0, 0}, question...)
	if kind == 1 {
		reply = append(reply, 0xc0, 0x0c, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x05, 0x00, 0x04, 127, 0, 0, 1)
	}
	return reply
}

// TestLatencyNeverIncludesNameResolving pins the resolver to a 90 ms answer so a
// second lookup inside the timed region would be impossible to miss.
func TestLatencyNeverIncludesNameResolving(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub resolver and the POSIX ping stub are Linux only")
	}
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "ping"), []byte("#!/bin/sh\nexit 0\n"), 0755); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	udp, e := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		t.Fatal(e)
	}
	defer udp.Close()
	resolver := &fixedDNS{conn: udp, delay: 90 * time.Millisecond}
	go resolver.serve()
	previous := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "udp", udp.LocalAddr().String())
	}}
	defer func() { net.DefaultResolver = previous }()

	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	go func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			conn.Close()
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port

	runner := &Runner{}
	observation := model.Observation{Healthy: true, Direct: true}
	task := func(m model.Monitor) model.Task {
		m.Timeout = 5
		return model.Task{Monitor: m, Expires: time.Now().Add(time.Minute).Unix()}
	}
	v := runner.Check(context.Background(), task(model.Monitor{Kind: "tcp", Address: "node.example", Port: port}), observation)
	if v.Outcome != "up" {
		t.Fatalf("the pinned answer must reach the listener: %+v", v)
	}
	if v.Latency > 60 {
		t.Fatalf("a resolver round trip leaked into the TCP latency: %v ms", v.Latency)
	}
	if got := resolver.count(); got != 1 {
		t.Fatalf("the name must be resolved once, got %d lookups", got)
	}

	v = runner.Check(context.Background(), task(model.Monitor{Kind: "tls", Address: "node.example", Port: port}), observation)
	if v.Stage != "tls_handshake" {
		t.Fatalf("the connection must reach the listener before the handshake fails: %+v", v)
	}
	v = runner.Check(context.Background(), task(model.Monitor{Kind: "http", URL: fmt.Sprintf("https://node.example:%d/", port)}), observation)
	if v.Stage != "proxy_request" {
		t.Fatalf("the request must reach the listener before it fails: %+v", v)
	}
	if got := resolver.count(); got != 3 {
		t.Fatalf("tls and http must reuse the resolved address too, got %d lookups in total", got)
	}
}
