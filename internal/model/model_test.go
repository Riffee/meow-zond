package model

import "testing"

func TestMonitorCheckableRequiresHostOrEnabledNode(t *testing.T) {
	base := Monitor{Source: "remnawave", HostManaged: true, HostDisabled: true}
	cases := []struct {
		name string
		m    Monitor
		want bool
	}{
		{"hidden host, enabled node", func() Monitor { m := base; m.NodeState = "on"; return m }(), true},
		{"hidden host, node error", func() Monitor { m := base; m.NodeState = "error"; return m }(), true},
		{"hidden host, disabled node", func() Monitor { m := base; m.NodeState = "off"; return m }(), false},
		{"hidden host, unknown node", base, false},
		{"visible host, disabled node", func() Monitor { m := base; m.HostDisabled = false; m.NodeState = "off"; return m }(), true},
		{"paused", func() Monitor { m := base; m.NodeState = "on"; m.Paused = true; return m }(), false},
		{"missing", func() Monitor { m := base; m.NodeState = "on"; m.Missing = true; return m }(), false},
		{"legacy node monitor", func() Monitor { m := base; m.NodeState = "on"; m.HostManaged = false; return m }(), false},
		{"manual", Monitor{Source: "manual"}, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.Checkable(); got != tt.want {
				t.Fatalf("Checkable() = %v, want %v: %+v", got, tt.want, tt.m)
			}
		})
	}
}

func TestMonitorNodeStatusFallback(t *testing.T) {
	cases := []struct {
		m    Monitor
		want string
	}{
		{Monitor{NodeState: "on", PanelState: "offline"}, "on"},
		{Monitor{NodeDisabled: true, PanelState: "connected"}, "off"},
		{Monitor{PanelState: "connected"}, "on"},
		{Monitor{PanelState: "offline"}, "error"},
		{Monitor{PanelState: "disabled"}, "off"},
		{Monitor{PanelState: "missing"}, ""},
	}
	for _, tt := range cases {
		if got := tt.m.NodeStatus(); got != tt.want {
			t.Fatalf("NodeStatus() = %q, want %q: %+v", got, tt.want, tt.m)
		}
	}
}
