package main

import "testing"

func TestRequestedServiceActionAllowsOneOperation(t *testing.T) {
	cases := []struct {
		name  string
		flags []bool
		want  string
		err   bool
	}{
		{"nothing", []bool{false, false, false, false, false, false, false, false}, "", false},
		{"install", []bool{true, false, false, false, false, false, false, false}, actionInstall, false},
		{"disable autostart", []bool{false, false, false, false, false, false, true, false}, actionDisable, false},
		{"two actions", []bool{true, false, false, false, false, false, false, true}, "", true},
	}
	for _, tc := range cases {
		got, e := requestedServiceAction(tc.flags...)
		if tc.err && e == nil {
			t.Errorf("%s: two service actions must be refused", tc.name)
		}
		if !tc.err && e != nil {
			t.Errorf("%s: %v", tc.name, e)
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestServiceSettledWaitsForTheRequestedState(t *testing.T) {
	running := serviceStatus{installed: true, running: true, autostart: true}
	stopped := serviceStatus{installed: true, autostart: true}
	off := serviceStatus{installed: true}
	gone := serviceStatus{}
	cases := []struct {
		action string
		status serviceStatus
		want   bool
	}{
		{actionInstall, running, true},
		{actionInstall, stopped, false},
		{actionStop, stopped, true},
		{actionStop, running, false},
		{actionDisable, stopped, false},
		{actionDisable, off, true},
		{actionStopDisabled, off, true},
		{actionStopDisabled, stopped, false},
		{actionUninstall, gone, true},
		{actionUninstall, off, true},
		{actionUninstall, stopped, false},
	}
	for _, tc := range cases {
		if got := serviceSettled(tc.action, tc.status); got != tc.want {
			t.Errorf("serviceSettled(%q, %+v) = %v, want %v", tc.action, tc.status, got, tc.want)
		}
	}
}

func TestServiceSummaryDistinguishesAutostart(t *testing.T) {
	cases := []struct {
		status serviceStatus
		want   string
	}{
		{serviceStatus{}, "Фоновая служба: не установлена"},
		{serviceStatus{installed: true, running: true, autostart: true}, "Фоновая служба: работает в фоне, автозапуск включён"},
		{serviceStatus{installed: true, running: true}, "Фоновая служба: работает в фоне, автозапуск выключен"},
		{serviceStatus{installed: true, autostart: true}, "Фоновая служба: остановлена, автозапуск включён"},
		{serviceStatus{installed: true}, "Фоновая служба: выключена"},
	}
	for _, tc := range cases {
		if got := serviceSummary(tc.status); got != tc.want {
			t.Errorf("serviceSummary(%+v) = %q, want %q", tc.status, got, tc.want)
		}
	}
}
