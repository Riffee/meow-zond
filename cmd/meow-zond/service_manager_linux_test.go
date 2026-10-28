//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"meowzond/internal/probe"
)

// systemdStub emulates the answers systemd gives through exit codes and keeps the
// unit state in a file, so install/start/stop/enable/disable can be verified without
// a real service manager.
const systemdStub = `#!/bin/sh
state="$MEOW_SYSTEMCTL_STATE"
log="$MEOW_SYSTEMCTL_LOG"
echo "systemctl $*" >> "$log"
enabled=0
active=0
[ -f "$state" ] && . "$state"
case "$1" in
	daemon-reload) exit 0 ;;
	enable) enabled=1; [ "$2" = "--now" ] && active=1 ;;
	disable) enabled=0; [ "$2" = "--now" ] && active=0 ;;
	start) active=1 ;;
	stop) active=0 ;;
	is-active)
		if [ "$active" = 1 ]; then echo active; exit 0; fi
		echo inactive; exit 3 ;;
	is-enabled)
		if [ "$enabled" = 1 ]; then echo enabled; exit 0; fi
		echo disabled; exit 1 ;;
	*) echo "unsupported: $*" >&2; exit 4 ;;
esac
printf 'enabled=%s\nactive=%s\n' "$enabled" "$active" > "$state"
exit 0
`

type systemdHarness struct {
	etc    string
	lib    string
	state  string
	binary string
	log    string
}

func newSystemdHarness(t *testing.T) systemdHarness {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("установка службы требует прав root")
	}
	root := t.TempDir()
	h := systemdHarness{
		etc:    filepath.Join(root, "etc", "systemd", "system"),
		lib:    filepath.Join(root, "lib", "systemd", "system"),
		state:  filepath.Join(root, "var", "lib", "meow-zond"),
		binary: filepath.Join(root, "usr", "local", "bin", "meow-zond"),
		log:    filepath.Join(root, "systemctl.log"),
	}
	for _, dir := range []string{h.etc, h.lib, h.state, filepath.Dir(h.binary), filepath.Join(root, "run", "systemd", "system")} {
		if e := os.MkdirAll(dir, 0755); e != nil {
			t.Fatal(e)
		}
	}
	bin := filepath.Join(root, "bin")
	if e := os.MkdirAll(bin, 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(bin, "systemctl"), []byte(systemdStub), 0755); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("MEOW_SYSTEMCTL_STATE", filepath.Join(root, "systemd.state"))
	t.Setenv("MEOW_SYSTEMCTL_LOG", h.log)

	// The real /etc is never touched: the unit, the program and the state directory
	// all live under the temporary root.
	previous := []string{serviceEtcDir, servicePackageDir, serviceBinaryPath, serviceStateDir, serviceRuntimeDir}
	serviceEtcDir, servicePackageDir, serviceBinaryPath = h.etc, h.lib, h.binary
	serviceStateDir, serviceRuntimeDir = h.state, filepath.Join(root, "run", "systemd", "system")
	t.Cleanup(func() {
		serviceEtcDir, servicePackageDir, serviceBinaryPath = previous[0], previous[1], previous[2]
		serviceStateDir, serviceRuntimeDir = previous[3], previous[4]
	})
	return h
}

func consoleConfig(t *testing.T, h systemdHarness) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "probe.json")
	c := probe.Config{
		Server:    "https://status.example.test",
		ProbeID:   "probe-1",
		ProbeName: "Домашний зонд",
		Token:     strings.Repeat("t", 64),
		Controls:  probe.DefaultControls(),
		Spool:     filepath.Join(h.state, "user-spool.json"),
	}
	if e := probe.Save(path, c); e != nil {
		t.Fatal(e)
	}
	return path
}

func systemdCalls(t *testing.T, h systemdHarness) string {
	t.Helper()
	out, e := os.ReadFile(h.log)
	if e != nil {
		return ""
	}
	return string(out)
}

func TestSystemdServiceInstallRunsAndSurvivesReboot(t *testing.T) {
	h := newSystemdHarness(t)
	configPath := consoleConfig(t, h)
	if !serviceSupported() {
		t.Fatal("stub systemctl must make the service manager visible")
	}
	if st, e := currentServiceStatus(); e != nil || st.installed {
		t.Fatalf("no unit expected before install: %+v %v", st, e)
	}
	if e := serviceInstall(configPath); e != nil {
		t.Fatal(e)
	}
	unit, e := os.ReadFile(unitFile())
	if e != nil {
		t.Fatal(e)
	}
	text := string(unit)
	if !strings.Contains(text, "ExecStart="+h.binary+" -headless -config "+filepath.Join(h.state, "probe.json")) {
		t.Fatalf("unit must start the installed binary with the service settings:\n%s", text)
	}
	if !strings.Contains(text, "WantedBy=multi-user.target") || !strings.Contains(text, "StateDirectory=meow-zond") {
		t.Fatalf("unit must be installable and own its state directory:\n%s", text)
	}
	if _, e = os.Stat(h.binary); e != nil {
		t.Fatalf("the program was not copied to a stable path: %v", e)
	}
	saved, e := probe.Load(filepath.Join(h.state, "probe.json"))
	if e != nil {
		t.Fatal(e)
	}
	if filepath.Dir(saved.Spool) != h.state {
		t.Fatalf("service must use its own spool, got %q", saved.Spool)
	}
	if info, e := os.Stat(filepath.Join(h.state, "probe.json")); e != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("token file must be readable only by root: %v %v", info.Mode(), e)
	}
	calls := systemdCalls(t, h)
	if !strings.Contains(calls, "systemctl enable --now "+serviceName) {
		t.Fatalf("install must enable and start the unit:\n%s", calls)
	}
	st, e := currentServiceStatus()
	if e != nil || !st.installed || !st.running || !st.autostart {
		t.Fatalf("installed service must run with autostart: %+v %v", st, e)
	}
	if e := serviceInstall(configPath); e == nil {
		t.Fatal("installing twice must be refused")
	}
}

func TestSystemdAutostartCanBeTurnedOffWithoutRemovingTheService(t *testing.T) {
	h := newSystemdHarness(t)
	configPath := consoleConfig(t, h)
	if e := serviceInstall(configPath); e != nil {
		t.Fatal(e)
	}
	if e := serviceSetAutostart(false); e != nil {
		t.Fatal(e)
	}
	st, e := currentServiceStatus()
	if e != nil || !st.installed || !st.running || st.autostart {
		t.Fatalf("disabling autostart must leave a running service: %+v %v", st, e)
	}
	if e := applyServiceAction(actionStopDisabled, configPath); e != nil {
		t.Fatal(e)
	}
	st, _ = currentServiceStatus()
	if st.running || st.autostart {
		t.Fatalf("stop-and-disable must leave nothing behind: %+v", st)
	}
	if !strings.Contains(systemdCalls(t, h), "systemctl disable "+serviceName) {
		t.Fatalf("autostart must be turned off with systemctl disable:\n%s", systemdCalls(t, h))
	}
	if e := serviceSetAutostart(true); e != nil {
		t.Fatal(e)
	}
	st, _ = currentServiceStatus()
	if !st.running || !st.autostart {
		t.Fatalf("re-enabling must start the unit again: %+v", st)
	}
}

func TestSystemdServiceSyncAppliesNewAddressAndRestart(t *testing.T) {
	h := newSystemdHarness(t)
	configPath := consoleConfig(t, h)
	if e := serviceInstall(configPath); e != nil {
		t.Fatal(e)
	}
	c, e := probe.Load(configPath)
	if e != nil {
		t.Fatal(e)
	}
	c.Server = "https://another-status.example.test"
	if e = probe.Save(configPath, c); e != nil {
		t.Fatal(e)
	}
	if e := serviceSync(configPath); e != nil {
		t.Fatal(e)
	}
	saved, e := probe.Load(filepath.Join(h.state, "probe.json"))
	if e != nil {
		t.Fatal(e)
	}
	if saved.Server != "https://another-status.example.test" {
		t.Fatalf("service settings kept the old address: %q", saved.Server)
	}
	st, _ := currentServiceStatus()
	if !st.running {
		t.Fatal("sync must leave the service running")
	}
}

func TestSystemdInstallEnablesThePackagedUnitInsteadOfWritingItsOwn(t *testing.T) {
	h := newSystemdHarness(t)
	configPath := consoleConfig(t, h)
	packaged := filepath.Join(h.lib, serviceName)
	unit := "[Service]\nExecStart=" + serviceBinaryPath + " -headless -config " + filepath.Join(h.state, "probe.json") + "\n\n[Install]\nWantedBy=multi-user.target\n"
	if e := os.WriteFile(packaged, []byte(unit), 0644); e != nil {
		t.Fatal(e)
	}
	if e := serviceInstall(configPath); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(unitFile()); !os.IsNotExist(e) {
		t.Fatalf("dpkg owns %s: the program must not shadow it: %v", packaged, e)
	}
	if _, e := os.Stat(filepath.Join(h.state, "probe.json")); e != nil {
		t.Fatalf("installing the packaged unit must still save the settings: %v", e)
	}
	if !strings.Contains(systemdCalls(t, h), "systemctl enable --now "+serviceName) {
		t.Fatalf("the packaged unit must be enabled:\n%s", systemdCalls(t, h))
	}
	st, _ := currentServiceStatus()
	if !st.installed || !st.running || !st.autostart {
		t.Fatalf("the packaged unit must run after install: %+v", st)
	}
	if e := serviceInstall(configPath); e == nil {
		t.Fatal("an enabled service must refuse a second install")
	}
}

// TestPackagedUnitMatchesGeneratedUnit keeps the file the .deb installs identical to
// the unit the program writes, so dpkg installs and "sudo -install-service" agree.
func TestPackagedUnitMatchesGeneratedUnit(t *testing.T) {
	dir := t.TempDir()
	serviceEtcDir = dir
	t.Cleanup(func() { serviceEtcDir = "/etc/systemd/system" })
	if e := writeUnit("/usr/bin/meow-zond", "/var/lib/meow-zond/probe.json"); e != nil {
		t.Fatal(e)
	}
	generated, e := os.ReadFile(unitFile())
	if e != nil {
		t.Fatal(e)
	}
	packaged, e := os.ReadFile(filepath.Join("..", "..", "assets", "meow-zond.service"))
	if e != nil {
		t.Skip("assets/ lives in the standalone probe repository only")
	}
	if string(generated) != string(packaged) {
		t.Fatalf("the packaged unit drifted from the generated one:\n--- packaged\n%s\n--- generated\n%s", packaged, generated)
	}
}

func TestSystemdRemoveStopsReporting(t *testing.T) {
	h := newSystemdHarness(t)
	configPath := consoleConfig(t, h)
	if e := serviceInstall(configPath); e != nil {
		t.Fatal(e)
	}
	if e := serviceRemove(); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(unitFile()); !os.IsNotExist(e) {
		t.Fatalf("the unit file must be gone: %v", e)
	}
	st, e := currentServiceStatus()
	if e != nil || st.installed || st.running || st.autostart {
		t.Fatalf("removed service must be invisible: %+v %v", st, e)
	}
	if !strings.Contains(systemdCalls(t, h), "systemctl disable --now "+serviceName) {
		t.Fatalf("removal must stop autostart first:\n%s", systemdCalls(t, h))
	}
}
