package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
)

func TestRestartProcessActionExplicitCommand(t *testing.T) {
	action := &restartProcessAction{}

	result, err := action.Invoke(pluginsdk.StateData{
		"name":    "sshd",
		"command": "echo disconnect",
	})
	if err != nil {
		t.Fatalf("Invoke returned error: %v", err)
	}

	if got := result.GetString("name"); got != "sh" {
		t.Fatalf("expected explicit command to use sh, got %q", got)
	}
	if got := result.GetStringList("args"); len(got) != 2 || got[0] != "-lc" || got[1] != "echo disconnect" {
		t.Fatalf("unexpected explicit command args: %#v", got)
	}
}

func TestRestartProcessActionSystemdManager(t *testing.T) {
	action := &restartProcessAction{}
	originalCmdExec := pluginsdk.CmdExec
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		return &pluginsdk.CmdResult{Stdout: "LoadState=loaded\nActiveState=active\nUnitFileState=enabled\n", ExitCode: 0}, nil
	}
	defer func() {
		pluginsdk.CmdExec = originalCmdExec
	}()

	result, err := action.Invoke(pluginsdk.StateData{
		"name":    "sshd",
		"manager": "systemd",
	})
	if err != nil {
		t.Fatalf("Invoke returned error: %v", err)
	}

	if got := result.GetString("name"); got != "systemctl" {
		t.Fatalf("unexpected systemd restart command name: %q", got)
	}
	if got := result.GetStringList("args"); len(got) != 2 || got[0] != "restart" || got[1] != "sshd.service" {
		t.Fatalf("unexpected systemd restart args: %#v", got)
	}
}

func TestServiceStateHelpers(t *testing.T) {
	t.Parallel()

	if !isEnabledUnitState("enabled-runtime") {
		t.Fatal("expected enabled-runtime to count as enabled")
	}
	if isEnabledUnitState("disabled") {
		t.Fatal("did not expect disabled to count as enabled")
	}
	if !isMaskedUnitState("masked-runtime") {
		t.Fatal("expected masked-runtime to count as masked")
	}
	if got := desiredStateFromActiveState("reloading"); got != "running" {
		t.Fatalf("unexpected desired state for reloading: %q", got)
	}
	if got := desiredStateFromActiveState("failed"); got != "stopped" {
		t.Fatalf("unexpected desired state for failed: %q", got)
	}
	if got := systemdReloadTriggerDigest(pluginsdk.StateData{"reload_triggers": []interface{}{"b", "a", "a", " "}}); got != "a\x00b" {
		t.Fatalf("unexpected reload trigger digest: %q", got)
	}
}

func TestSystemdUnitUpdateReloadsOnTriggerChangeWhenActive(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origFileExists := pluginsdk.FileExists
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.FileExists = origFileExists
	})

	commands := make([]string, 0, 4)
	pluginsdk.FileExists = func(path string) (bool, error) {
		return false, nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		commands = append(commands, cmd+" "+strings.Join(args, " "))
		if cmd != "systemctl" {
			t.Fatalf("unexpected command: %s %#v", cmd, args)
		}
		if len(args) >= 1 && args[0] == "show" {
			return &pluginsdk.CmdResult{Stdout: "LoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\n", ExitCode: 0}, nil
		}
		if len(args) == 2 && args[0] == "reload" && args[1] == "nginx.service" {
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		}
		return &pluginsdk.CmdResult{ExitCode: 0}, nil
	}

	state, err := (&systemdUnitResource{}).Update(
		pluginsdk.StateData{"name": "nginx", "reload_on_change": true, "reload_triggers": []interface{}{"old"}},
		pluginsdk.StateData{"name": "nginx", "reload_on_change": true, "reload_triggers": []interface{}{"new"}},
	)
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if len(commands) != 3 || !strings.HasPrefix(commands[0], "systemctl show ") || commands[1] != "systemctl reload nginx.service" || !strings.HasPrefix(commands[2], "systemctl show ") {
		t.Fatalf("unexpected commands: %#v", commands)
	}
	if !state.GetBool("reload_on_change") {
		t.Fatalf("expected reload_on_change to round-trip in state: %#v", state)
	}
	if got := state.GetStringList("reload_triggers"); len(got) != 1 || got[0] != "new" {
		t.Fatalf("expected reload_triggers to round-trip in state: %#v", state)
	}
}

func TestSystemdUnitInfoDataSource(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origHasCommand := pluginsdk.HasCommand
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.HasCommand = origHasCommand
	})

	pluginsdk.HasCommand = func(name string) (bool, error) {
		return name == "systemctl", nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		if cmd != "systemctl" {
			t.Fatalf("unexpected command: %s %#v", cmd, args)
		}
		return &pluginsdk.CmdResult{
			Stdout: strings.Join([]string{
				"LoadState=loaded",
				"ActiveState=active",
				"SubState=running",
				"UnitFileState=enabled",
				"",
			}, "\n"),
			ExitCode: 0,
		}, nil
	}

	state, err := (&systemdUnitInfoDataSource{}).DataRead(pluginsdk.StateData{"name": "kubelet"})
	if err != nil {
		t.Fatalf("DataRead returned error: %v", err)
	}
	if state.GetString("name") != "kubelet.service" {
		t.Fatalf("expected normalized unit name, got %#v", state)
	}
	if state.GetString("sub_state") != "running" {
		t.Fatalf("expected sub_state to be populated, got %#v", state)
	}
	if !state.GetBool("enabled") || state.GetBool("masked") {
		t.Fatalf("unexpected enabled/masked state: %#v", state)
	}
}

func TestTimezoneResourceCreateAppliesTimezone(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
	})

	currentZone := "Etc/UTC"
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		if cmd != "timedatectl" {
			t.Fatalf("unexpected command: %s %#v", cmd, args)
		}
		switch {
		case len(args) == 2 && args[0] == "set-timezone":
			currentZone = args[1]
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		case len(args) == 2 && args[0] == "show" && args[1] == "--property=Timezone":
			return &pluginsdk.CmdResult{}, nil
		case len(args) == 3 && args[0] == "show" && args[1] == "--property=Timezone" && args[2] == "--value":
			return &pluginsdk.CmdResult{Stdout: currentZone + "\n", ExitCode: 0}, nil
		default:
			t.Fatalf("unexpected timedatectl args: %#v", args)
			return nil, nil
		}
	}

	state, err := (&timezoneResource{}).Create(pluginsdk.StateData{"zone": "UTC"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if got := state.GetString("zone"); got != "UTC" {
		t.Fatalf("zone = %q, want UTC", got)
	}
	if got := state.GetString("id"); got != "timezone" {
		t.Fatalf("id = %q, want timezone", got)
	}
}

func TestValidateSystemdUnitContentUsesTmpPath(t *testing.T) {
	origHasCommand := pluginsdk.HasCommand
	origFileWrite := pluginsdk.FileWrite
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.HasCommand = origHasCommand
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.CmdExec = origCmdExec
	})

	var wrotePath string
	pluginsdk.HasCommand = func(name string) (bool, error) {
		return name == "systemd-analyze", nil
	}
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		wrotePath = path
		return nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		if cmd == "systemd-analyze" {
			if len(args) != 2 || args[0] != "verify" {
				t.Fatalf("unexpected systemd-analyze args: %#v", args)
			}
			if strings.HasPrefix(args[1], unitDir+"/") {
				t.Fatalf("validation should not write into %s, got %q", unitDir, args[1])
			}
			if !strings.HasPrefix(args[1], "/tmp/") {
				t.Fatalf("expected temp validation path under /tmp, got %q", args[1])
			}
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		}
		if cmd == "rm" {
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		}
		t.Fatalf("unexpected command: %s %#v", cmd, args)
		return nil, nil
	}

	if err := validateSystemdUnitContent("nginx", "[Service]\nExecStart=/usr/bin/nginx\n"); err != nil {
		t.Fatalf("validateSystemdUnitContent returned error: %v", err)
	}
	if wrotePath == "" {
		t.Fatal("expected validation temp file to be written")
	}
	if !strings.HasPrefix(wrotePath, "/tmp/") {
		t.Fatalf("expected temp validation file under /tmp, got %q", wrotePath)
	}
	if !strings.HasSuffix(wrotePath, ".service") {
		t.Fatalf("expected service validation temp file to retain .service suffix, got %q", wrotePath)
	}
}

func TestValidateSystemdUnitContentPreservesExplicitUnitSuffix(t *testing.T) {
	origHasCommand := pluginsdk.HasCommand
	origFileWrite := pluginsdk.FileWrite
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.HasCommand = origHasCommand
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.CmdExec = origCmdExec
	})

	var wrotePath string
	pluginsdk.HasCommand = func(name string) (bool, error) {
		return name == "systemd-analyze", nil
	}
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		wrotePath = path
		return nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		switch cmd {
		case "systemd-analyze", "rm":
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		default:
			t.Fatalf("unexpected command: %s %#v", cmd, args)
			return nil, nil
		}
	}

	if err := validateSystemdUnitContent("nightly.timer", "[Timer]\nOnCalendar=daily\n"); err != nil {
		t.Fatalf("validateSystemdUnitContent returned error: %v", err)
	}
	if !strings.HasSuffix(wrotePath, ".timer") {
		t.Fatalf("expected timer validation temp file to retain .timer suffix, got %q", wrotePath)
	}
}

func TestSystemdUnitValidateRejectsMaskedRunning(t *testing.T) {
	origHasCommand := pluginsdk.HasCommand
	t.Cleanup(func() {
		pluginsdk.HasCommand = origHasCommand
	})

	pluginsdk.HasCommand = func(name string) (bool, error) {
		return name == "systemctl", nil
	}

	err := (&systemdUnitResource{}).Validate(pluginsdk.StateData{
		"name":   "nginx",
		"masked": true,
		"state":  "running",
	})
	if err == nil || !strings.Contains(err.Error(), "masked units cannot have state \"running\"") {
		t.Fatalf("expected masked/running validation error, got %v", err)
	}
}

func TestTimezoneValidateRequiresTimedatectlAndKnownZone(t *testing.T) {
	origHasCommand := pluginsdk.HasCommand
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.HasCommand = origHasCommand
		pluginsdk.CmdExec = origCmdExec
	})

	pluginsdk.HasCommand = func(name string) (bool, error) {
		return name == "timedatectl", nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		if cmd != "timedatectl" || len(args) != 1 || args[0] != "list-timezones" {
			t.Fatalf("unexpected timedatectl command: %s %#v", cmd, args)
		}
		return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "UTC\nAmerica/New_York\n"}, nil
	}

	if err := (&timezoneResource{}).Validate(pluginsdk.StateData{"zone": "UTC"}); err != nil {
		t.Fatalf("expected known timezone to validate, got %v", err)
	}
	if err := (&timezoneResource{}).Validate(pluginsdk.StateData{"zone": "Mars/Phobos"}); err == nil || !strings.Contains(err.Error(), "is not available") {
		t.Fatalf("expected unavailable timezone error, got %v", err)
	}

	pluginsdk.HasCommand = func(string) (bool, error) {
		return false, nil
	}
	if err := (&timezoneResource{}).Validate(pluginsdk.StateData{"zone": "UTC"}); err == nil || !strings.Contains(err.Error(), "requires timedatectl") {
		t.Fatalf("expected missing timedatectl error, got %v", err)
	}
}

func TestSystemdUnitCreateWritesReloadsAndStarts(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origFileWrite := pluginsdk.FileWrite
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileWrite = origFileWrite
	})

	content := "[Service]\nExecStart=/usr/bin/nginx\n"
	unitPath := unitFilePath("nginx")
	var readCalls int
	pluginsdk.FileExists = func(path string) (bool, error) {
		return path == unitPath, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		if path != unitPath {
			t.Fatalf("unexpected read path: %q", path)
		}
		readCalls++
		if readCalls == 1 {
			return nil, errors.New("missing unit file")
		}
		return []byte(content), nil
	}
	var wrotePath string
	var wroteData []byte
	var wroteMode uint32
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		wrotePath = path
		wroteData = append([]byte(nil), data...)
		wroteMode = mode
		return nil
	}
	var commands []string
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		commands = append(commands, cmd+" "+strings.Join(args, " "))
		if cmd != "systemctl" {
			t.Fatalf("unexpected command: %s %#v", cmd, args)
		}
		switch {
		case len(args) == 1 && args[0] == "daemon-reload":
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		case len(args) == 2 && args[0] == "enable" && args[1] == "nginx.service":
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		case len(args) == 2 && args[0] == "start" && args[1] == "nginx.service":
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		case len(args) == 4 && args[0] == "show":
			return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "LoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\n"}, nil
		default:
			t.Fatalf("unexpected systemctl args: %#v", args)
			return nil, nil
		}
	}

	state, err := (&systemdUnitResource{}).Create(pluginsdk.StateData{
		"name":    "nginx",
		"content": content,
		"enabled": true,
		"state":   "running",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if wrotePath != unitPath {
		t.Fatalf("wrote path = %q, want %q", wrotePath, unitPath)
	}
	if string(wroteData) != content {
		t.Fatalf("wrote data = %q, want %q", string(wroteData), content)
	}
	if wroteMode != 0o644 {
		t.Fatalf("wrote mode = %#o, want %#o", wroteMode, uint32(0o644))
	}
	if got := state.GetString("active_state"); got != "active" {
		t.Fatalf("active_state = %q, want active", got)
	}
	if !state.GetBool("enabled") || state.GetString("state") != "running" {
		t.Fatalf("unexpected returned state: %#v", state)
	}
	if got := state.GetString("content"); got != content {
		t.Fatalf("content = %q, want %q", got, content)
	}
	if len(commands) != 4 {
		t.Fatalf("expected 4 systemctl commands, got %#v", commands)
	}
}

func TestSystemdUnitDeleteUnmasksStopsDisablesAndRemovesContent(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origFileDelete := pluginsdk.FileDelete
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.FileDelete = origFileDelete
	})

	var commands []string
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		commands = append(commands, cmd+" "+strings.Join(args, " "))
		if cmd != "systemctl" {
			t.Fatalf("unexpected command: %s %#v", cmd, args)
		}
		return &pluginsdk.CmdResult{ExitCode: 0}, nil
	}
	var deletedPath string
	pluginsdk.FileDelete = func(path string) error {
		deletedPath = path
		return nil
	}

	err := (&systemdUnitResource{}).Delete(pluginsdk.StateData{
		"name":    "nginx",
		"masked":  true,
		"content": "[Service]\nExecStart=/usr/bin/nginx\n",
	})
	if err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	wantCommands := []string{
		"systemctl unmask nginx.service",
		"systemctl stop nginx.service",
		"systemctl disable nginx.service",
		"systemctl daemon-reload",
	}
	if !reflect.DeepEqual(commands, wantCommands) {
		t.Fatalf("unexpected commands:\nwant %#v\n got %#v", wantCommands, commands)
	}
	if deletedPath != unitFilePath("nginx") {
		t.Fatalf("deleted path = %q, want %q", deletedPath, unitFilePath("nginx"))
	}
}

func TestSystemdAndTimezoneImportState(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origFileExists := pluginsdk.FileExists
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.FileExists = origFileExists
	})

	if _, err := (&systemdUnitResource{}).ImportState(""); err == nil || !strings.Contains(err.Error(), "must be a unit name") {
		t.Fatalf("expected systemd import ID error, got %v", err)
	}
	if _, err := (&timezoneResource{}).ImportState("custom"); err == nil || !strings.Contains(err.Error(), "must be empty") {
		t.Fatalf("expected timezone import ID error, got %v", err)
	}

	pluginsdk.FileExists = func(string) (bool, error) {
		return false, nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		switch {
		case cmd == "systemctl" && len(args) == 4 && args[0] == "show":
			return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "LoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\n"}, nil
		case cmd == "timedatectl" && len(args) == 3 && args[0] == "show":
			return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "UTC\n"}, nil
		default:
			t.Fatalf("unexpected command: %s %#v", cmd, args)
			return nil, nil
		}
	}

	unitState, err := (&systemdUnitResource{}).ImportState("nginx")
	if err != nil {
		t.Fatalf("systemd ImportState returned error: %v", err)
	}
	if got := unitState.GetString("id"); got != "nginx" {
		t.Fatalf("systemd import id = %q, want nginx", got)
	}
	zoneState, err := (&timezoneResource{}).ImportState("host")
	if err != nil {
		t.Fatalf("timezone ImportState returned error: %v", err)
	}
	if got := zoneState.GetString("zone"); got != "UTC" {
		t.Fatalf("timezone zone = %q, want UTC", got)
	}
}

func TestResolveRestartManagerFallsBackToServiceAndErrorsWithoutManagers(t *testing.T) {
	origHasCommand := pluginsdk.HasCommand
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.HasCommand = origHasCommand
		pluginsdk.CmdExec = origCmdExec
	})

	pluginsdk.HasCommand = func(name string) (bool, error) {
		return name == "systemctl" || name == "service", nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		if cmd != "systemctl" || len(args) != 4 || args[0] != "show" {
			t.Fatalf("unexpected command: %s %#v", cmd, args)
		}
		return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "LoadState=not-found\nActiveState=inactive\nSubState=dead\nUnitFileState=\n"}, nil
	}

	manager, err := resolveRestartManager("nginx")
	if err != nil {
		t.Fatalf("resolveRestartManager returned error: %v", err)
	}
	if manager != "service" {
		t.Fatalf("manager = %q, want service", manager)
	}

	pluginsdk.HasCommand = func(string) (bool, error) {
		return false, nil
	}
	if _, err := resolveRestartManager("nginx"); err == nil || !strings.Contains(err.Error(), "no restart manager resolved") {
		t.Fatalf("expected no-manager error, got %v", err)
	}
}

func TestShellQuoteEscapesSingleQuotes(t *testing.T) {
	t.Parallel()

	if got := shellQuote("it's complicated"); got != `'it'"'"'s complicated'` {
		t.Fatalf("shellQuote returned %q", got)
	}
}
