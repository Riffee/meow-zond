package probe

import "testing"

func TestSelectNetworkPath(t *testing.T) {
	tests := []struct {
		name      string
		routes    []routeEntry
		active    string
		expected  string
		goos      string
		vpn       bool
		available bool
		bind      bool
		iface     string
	}{
		{
			name:      "direct physical route",
			routes:    []routeEntry{{Interface: "eth0", Metric: 100, Family: 4, Physical: true}},
			goos:      "linux",
			available: true,
			iface:     "eth0",
		},
		{
			name: "VPN full tunnel has no original route",
			routes: []routeEntry{
				{Interface: "tun0", Metric: 1, Family: 4},
			},
			goos:      "linux",
			vpn:       true,
			available: false,
		},
		{
			name: "VPN with physical route is bound to physical",
			routes: []routeEntry{
				{Interface: "tun0", Metric: 1, Family: 4},
				{Interface: "eth0", Metric: 100, Family: 4, Physical: true},
			},
			goos:      "linux",
			vpn:       true,
			available: true,
			bind:      true,
			iface:     "eth0",
		},
		{
			name: "windows virtual adapter is treated as VPN",
			routes: []routeEntry{
				{Interface: "Ethernet 2", Metric: 1, Family: 4, Physical: false},
			},
			goos:      "windows",
			vpn:       true,
			available: false,
		},
		{
			name: "windows physical route wins over virtual",
			routes: []routeEntry{
				{Interface: "Ethernet 2", Metric: 1, Family: 4, Physical: false},
				{Interface: "Ethernet", Metric: 50, Family: 4, Physical: true},
			},
			goos:      "windows",
			vpn:       true,
			available: true,
			bind:      true,
			iface:     "Ethernet",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := selectNetworkPath(tt.routes, tt.active, tt.expected, tt.goos)
			if path.VPN != tt.vpn || path.Available != tt.available || path.Bind != tt.bind || path.Interface != tt.iface {
				t.Fatalf("got %+v", path)
			}
		})
	}
}
