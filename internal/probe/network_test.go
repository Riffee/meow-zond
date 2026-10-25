package probe

import "testing"

func TestParseLinuxDefaultRoutes(t *testing.T) {
	routes, err := parseIPRoutes([]byte(`[
		{"dst":"default","gateway":"192.0.2.1","dev":"eth0","metric":100},
		{"dst":"default","gateway":"198.51.100.1","dev":"tun0","metric":5}
	]`), 4)
	if err != nil || len(routes) != 2 {
		t.Fatalf("routes: %+v, %v", routes, err)
	}
	if routes[0].Interface != "eth0" || !routes[0].Physical || routes[1].Physical {
		t.Fatalf("physical classification: %+v", routes)
	}
	if _, err := parseIPRoutes([]byte(`not json`), 4); err == nil {
		t.Fatal("invalid route JSON accepted")
	}
}

func TestWindowsRoutePayloadPhysicalClassification(t *testing.T) {
	payload := []byte(`{"routes":[{"interface":"Ethernet","index":7,"metric":25,"family":4,"physical":true},{"interface":"VPN Tunnel","index":31,"metric":1,"family":4,"physical":false}],"physical":["Ethernet"]}`)
	if got := classifyTestWindowsRoutes(t, payload); got != "Ethernet" {
		t.Fatal(got)
	}
}

func classifyTestWindowsRoutes(t *testing.T, payload []byte) string {
	t.Helper()
	var p windowsRoutePayload
	if err := jsonUnmarshal(payload, &p); err != nil {
		t.Fatal(err)
	}
	for _, route := range p.Routes {
		if route.Physical && !tunnelNames.MatchString(route.Interface) {
			return route.Interface
		}
	}
	return ""
}
