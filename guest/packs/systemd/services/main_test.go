package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	pluginsdk "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
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

func TestParseTimesyncdConfig(t *testing.T) {
	t.Parallel()

	servers, fallbackServers := parseTimesyncdConfig(`
# comment
[Time]
NTP=0.pool.ntp.org 1.pool.ntp.org
FallbackNTP=2.pool.ntp.org 3.pool.ntp.org
`)

	if got := strings.Join(servers, ","); got != "0.pool.ntp.org,1.pool.ntp.org" {
		t.Fatalf("unexpected servers: %q", got)
	}
	if got := strings.Join(fallbackServers, ","); got != "2.pool.ntp.org,3.pool.ntp.org" {
		t.Fatalf("unexpected fallback servers: %q", got)
	}
}

func TestNormalizeTimesyncServersRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{name: "empty", values: []string{""}, want: "must not contain empty values"},
		{name: "whitespace", values: []string{"0.pool.ntp.org extra"}, want: "must not contain whitespace"},
		{name: "comment", values: []string{"0.pool.ntp.org#bad"}, want: "must not contain comment markers"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeTimesyncServers(test.values, "servers")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("normalizeTimesyncServers(%#v) error = %v, want substring %q", test.values, err, test.want)
			}
		})
	}
}

func TestTimesyncResourceCreateWritesConfigAndRestartsService(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origFileWrite := pluginsdk.FileWrite
	origHasCommand := pluginsdk.HasCommand
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.HasCommand = origHasCommand
	})

	var configContent string
	commands := make([]string, 0, 8)
	serviceActive := true
	serviceEnabled := true

	pluginsdk.HasCommand = func(name string) (bool, error) {
		return name == "systemctl", nil
	}
	pluginsdk.FileExists = func(path string) (bool, error) {
		return path == timesyncdConfigPath && configContent != "", nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		if path == timesyncdConfigPath && configContent != "" {
			return []byte(configContent), nil
		}
		return nil, errors.New("missing file")
	}
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		if path != timesyncdConfigPath {
			t.Fatalf("unexpected write path: %s", path)
		}
		configContent = string(data)
		return nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		commands = append(commands, cmd+" "+strings.Join(args, " "))
		switch {
		case cmd == "mkdir" && len(args) == 2 && args[0] == "-p" && args[1] == timesyncdConfigDir:
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		case cmd == "systemctl" && len(args) == 3 && args[0] == "show" && args[1] == "--property=Version" && args[2] == "--value":
			return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "255\n"}, nil
		case cmd == "systemctl" && len(args) == 4 && args[0] == "show":
			activeState := "active"
			if !serviceActive {
				activeState = "inactive"
			}
			unitFileState := "enabled"
			if !serviceEnabled {
				unitFileState = "disabled"
			}
			return &pluginsdk.CmdResult{
				Stdout:   "LoadState=loaded\nActiveState=" + activeState + "\nSubState=running\nUnitFileState=" + unitFileState + "\n",
				ExitCode: 0,
			}, nil
		case cmd == "systemctl" && len(args) == 2 && args[0] == "enable" && args[1] == timesyncdServiceName:
			serviceEnabled = true
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		case cmd == "systemctl" && len(args) == 2 && args[0] == "restart" && args[1] == timesyncdServiceName:
			serviceActive = true
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		default:
			t.Fatalf("unexpected command: %s %#v", cmd, args)
			return nil, nil
		}
	}

	state, err := (&timesyncResource{}).Create(pluginsdk.StateData{
		"servers":          []interface{}{"0.pool.ntp.org", "1.pool.ntp.org"},
		"fallback_servers": []interface{}{"2.pool.ntp.org"},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if !state.GetBool("enabled") {
		t.Fatalf("expected timesync state to be enabled: %#v", state)
	}
	if got := state.GetString("backend"); got != timesyncBackendName {
		t.Fatalf("backend = %q, want %q", got, timesyncBackendName)
	}
	if got := strings.Join(state.GetStringList("servers"), ","); got != "0.pool.ntp.org,1.pool.ntp.org" {
		t.Fatalf("unexpected servers in state: %q", got)
	}
	if got := strings.Join(state.GetStringList("fallback_servers"), ","); got != "2.pool.ntp.org" {
		t.Fatalf("unexpected fallback servers in state: %q", got)
	}
	if !strings.Contains(configContent, "NTP=0.pool.ntp.org 1.pool.ntp.org") {
		t.Fatalf("expected config to contain primary servers, got %q", configContent)
	}
	if !strings.Contains(configContent, "FallbackNTP=2.pool.ntp.org") {
		t.Fatalf("expected config to contain fallback servers, got %q", configContent)
	}
	if !containsCommand(commands, "systemctl enable "+timesyncdServiceName) {
		t.Fatalf("expected enable command, got %#v", commands)
	}
	if !containsCommand(commands, "systemctl restart "+timesyncdServiceName) {
		t.Fatalf("expected restart command, got %#v", commands)
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

func TestSystemdUnitValidateServiceIdentity(t *testing.T) {
	origHasCommand := pluginsdk.HasCommand
	t.Cleanup(func() {
		pluginsdk.HasCommand = origHasCommand
	})

	pluginsdk.HasCommand = func(name string) (bool, error) {
		return name == "systemctl", nil
	}

	tests := []struct {
		name   string
		config pluginsdk.StateData
		want   string
	}{
		{
			name: "content conflict",
			config: pluginsdk.StateData{
				"name":         "nginx",
				"content":      "[Service]\nExecStart=/usr/bin/nginx\n",
				"service_user": "www-data",
			},
			want: "content cannot be used with service_user or service_group",
		},
		{
			name:   "timer unit",
			config: pluginsdk.StateData{"name": "nightly.timer", "service_user": "backup"},
			want:   "can only be used with .service units",
		},
		{
			name:   "empty user",
			config: pluginsdk.StateData{"name": "nginx", "service_user": "   "},
			want:   "service_user must not be empty",
		},
		{
			name:   "group whitespace",
			config: pluginsdk.StateData{"name": "nginx", "service_group": "web users"},
			want:   "service_group must not contain whitespace",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := (&systemdUnitResource{}).Validate(test.config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestServiceIdentityDropInHelpers(t *testing.T) {
	t.Parallel()

	path := serviceIdentityDropInPath("nginx")
	if path != "/etc/systemd/system/nginx.service.d/terraform-service-identity.override.conf" {
		t.Fatalf("drop-in path = %q", path)
	}
	rendered := renderServiceIdentityDropIn("app", "www-data")
	if !strings.HasPrefix(rendered, serviceIdentityDropInManagedLabel()+"\n") {
		t.Fatalf("expected managed label in rendered drop-in, got %q", rendered)
	}
	if !strings.Contains(rendered, "Managed by terraform_provider_linux") {
		t.Fatalf("expected provider marker in rendered drop-in, got %q", rendered)
	}
	if strings.Contains(rendered, "tf-linux-provider") {
		t.Fatalf("rendered drop-in contains retired project name: %q", rendered)
	}
	if isManagedServiceIdentityDropIn("[Service]\n# Managed by terraform_provider_linux. Changes will be overwritten.\n") {
		t.Fatal("managed marker should only be accepted as the first non-blank line")
	}
	if got := normalizeServiceIdentityProviderName("ubuntu"); got != "terraform_provider_ubuntu" {
		t.Fatalf("normalizeServiceIdentityProviderName(ubuntu) = %q", got)
	}
	if got := normalizeServiceIdentityProviderName("terraform_provider_ubuntu"); got != "terraform_provider_ubuntu" {
		t.Fatalf("normalizeServiceIdentityProviderName(prefixed) = %q", got)
	}
	user, group := parseServiceIdentityDropIn(rendered)
	if user != "app" || group != "www-data" {
		t.Fatalf("parsed user/group = %q/%q, want app/www-data", user, group)
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

func TestSystemdUnitCreateWritesServiceIdentityDropInAndRestartsActiveService(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origFileWrite := pluginsdk.FileWrite
	origDirEnsure := pluginsdk.DirEnsure
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.DirEnsure = origDirEnsure
	})

	dropInPath := serviceIdentityDropInPath("nginx")
	dropInDir := serviceIdentityDropInDir("nginx")
	files := map[string]string{}
	var ensuredDir string
	var ensuredMode uint32
	pluginsdk.DirEnsure = func(path string, mode uint32) error {
		ensuredDir = path
		ensuredMode = mode
		return nil
	}
	pluginsdk.FileExists = func(path string) (bool, error) {
		_, ok := files[path]
		return ok, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		if data, ok := files[path]; ok {
			return []byte(data), nil
		}
		return nil, errors.New("missing file")
	}
	var wrotePath string
	var wroteMode uint32
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		wrotePath = path
		wroteMode = mode
		files[path] = string(data)
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
		case len(args) == 2 && args[0] == "restart" && args[1] == "nginx.service":
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		case len(args) == 4 && args[0] == "show" && args[2] == "User,Group,DropInPaths":
			return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "User=app\nGroup=www-data\nDropInPaths=" + dropInPath + "\n"}, nil
		case len(args) == 4 && args[0] == "show":
			return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "LoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\n"}, nil
		default:
			t.Fatalf("unexpected systemctl args: %#v", args)
			return nil, nil
		}
	}

	state, err := (&systemdUnitResource{}).Create(pluginsdk.StateData{
		"name":          "nginx",
		"service_user":  "app",
		"service_group": "www-data",
		"enabled":       true,
		"state":         "running",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if ensuredDir != dropInDir || ensuredMode != 0o755 {
		t.Fatalf("DirEnsure = %q %#o, want %q %#o", ensuredDir, ensuredMode, dropInDir, uint32(0o755))
	}
	if wrotePath != dropInPath || wroteMode != 0o644 {
		t.Fatalf("FileWrite = %q %#o, want %q %#o", wrotePath, wroteMode, dropInPath, uint32(0o644))
	}
	if got := files[dropInPath]; !strings.Contains(got, "User=app") || !strings.Contains(got, "Group=www-data") {
		t.Fatalf("unexpected drop-in content: %q", got)
	}
	if got := state.GetString("service_user"); got != "app" {
		t.Fatalf("service_user = %q, want app", got)
	}
	if got := state.GetString("service_group"); got != "www-data" {
		t.Fatalf("service_group = %q, want www-data", got)
	}
	if got := state.GetString("service_identity_dropin_path"); got != dropInPath {
		t.Fatalf("service_identity_dropin_path = %q, want %q", got, dropInPath)
	}
	for _, want := range []string{
		"systemctl daemon-reload",
		"systemctl show --property User,Group,DropInPaths nginx.service",
		"systemctl enable nginx.service",
		"systemctl restart nginx.service",
	} {
		if !containsCommand(commands, want) {
			t.Fatalf("expected command %q, got %#v", want, commands)
		}
	}
}

func TestSystemdUnitCreateRollsBackServiceIdentityDropInOnEffectiveMismatch(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origFileWrite := pluginsdk.FileWrite
	origFileDelete := pluginsdk.FileDelete
	origDirEnsure := pluginsdk.DirEnsure
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.FileDelete = origFileDelete
		pluginsdk.DirEnsure = origDirEnsure
	})

	dropInPath := serviceIdentityDropInPath("nginx")
	files := map[string]string{}
	pluginsdk.DirEnsure = func(string, uint32) error { return nil }
	pluginsdk.FileExists = func(path string) (bool, error) {
		_, ok := files[path]
		return ok, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		if data, ok := files[path]; ok {
			return []byte(data), nil
		}
		return nil, errors.New("missing file")
	}
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		files[path] = string(data)
		return nil
	}
	var deletedPath string
	pluginsdk.FileDelete = func(path string) error {
		deletedPath = path
		delete(files, path)
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
		case len(args) == 4 && args[0] == "show" && args[2] == "User,Group,DropInPaths":
			return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "User=other\nGroup=www-data\nDropInPaths=" + dropInPath + " /etc/systemd/system/nginx.service.d/zz-local.conf\n"}, nil
		default:
			t.Fatalf("unexpected systemctl args: %#v", args)
			return nil, nil
		}
	}

	_, err := (&systemdUnitResource{}).Create(pluginsdk.StateData{
		"name":          "nginx",
		"service_user":  "app",
		"service_group": "www-data",
		"state":         "running",
	})
	if err == nil || !strings.Contains(err.Error(), "systemd resolved service identity") || !strings.Contains(err.Error(), "zz-local.conf") {
		t.Fatalf("expected effective identity mismatch error with drop-in detail, got %v", err)
	}
	if deletedPath != dropInPath {
		t.Fatalf("deleted path = %q, want %q", deletedPath, dropInPath)
	}
	if _, ok := files[dropInPath]; ok {
		t.Fatalf("expected rollback to delete newly-created drop-in, got %q", files[dropInPath])
	}
	if containsCommand(commands, "systemctl restart nginx.service") || containsCommand(commands, "systemctl start nginx.service") {
		t.Fatalf("service should not be restarted after identity mismatch: %#v", commands)
	}
	if got := countCommand(commands, "systemctl daemon-reload"); got != 2 {
		t.Fatalf("daemon-reload count = %d, want 2; commands %#v", got, commands)
	}
}

func TestSystemdUnitCreateRejectsUnmanagedServiceIdentityDropIn(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origFileWrite := pluginsdk.FileWrite
	origDirEnsure := pluginsdk.DirEnsure
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.DirEnsure = origDirEnsure
	})

	dropInPath := serviceIdentityDropInPath("nginx")
	pluginsdk.FileExists = func(path string) (bool, error) {
		return path == dropInPath, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		if path != dropInPath {
			t.Fatalf("unexpected read path: %q", path)
		}
		return []byte("[Service]\nUser=manual\n"), nil
	}
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		t.Fatalf("FileWrite should not be called for unmanaged conflict: %s", path)
		return nil
	}
	pluginsdk.DirEnsure = func(path string, mode uint32) error {
		t.Fatalf("DirEnsure should not be called for unmanaged conflict: %s", path)
		return nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		t.Fatalf("CmdExec should not be called for unmanaged conflict: %s %#v", cmd, args)
		return nil, nil
	}

	_, err := (&systemdUnitResource{}).Create(pluginsdk.StateData{
		"name":         "nginx",
		"service_user": "app",
	})
	if err == nil || !strings.Contains(err.Error(), "already exists and is not managed") {
		t.Fatalf("expected unmanaged drop-in conflict error, got %v", err)
	}
}

func TestSystemdUnitCreateRejectsUnexpectedManagedServiceIdentityDropIn(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origFileWrite := pluginsdk.FileWrite
	origDirEnsure := pluginsdk.DirEnsure
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.DirEnsure = origDirEnsure
	})

	dropInPath := serviceIdentityDropInPath("nginx")
	pluginsdk.FileExists = func(path string) (bool, error) {
		return path == dropInPath, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		if path != dropInPath {
			t.Fatalf("unexpected read path: %q", path)
		}
		return []byte(renderServiceIdentityDropIn("app", "www-data")), nil
	}
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		t.Fatalf("FileWrite should not be called for unexpected managed drop-in: %s", path)
		return nil
	}
	pluginsdk.DirEnsure = func(path string, mode uint32) error {
		t.Fatalf("DirEnsure should not be called for unexpected managed drop-in: %s", path)
		return nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		t.Fatalf("CmdExec should not be called for unexpected managed drop-in: %s %#v", cmd, args)
		return nil, nil
	}

	_, err := (&systemdUnitResource{}).Create(pluginsdk.StateData{
		"name":         "nginx",
		"service_user": "app",
	})
	if err == nil || !strings.Contains(err.Error(), "already exists unexpectedly") {
		t.Fatalf("expected unexpected managed drop-in error, got %v", err)
	}
}

func TestSystemdUnitReadRejectsUnexpectedServiceIdentityDropIn(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
	})

	dropInPath := serviceIdentityDropInPath("nginx")
	pluginsdk.FileExists = func(path string) (bool, error) {
		return path == dropInPath, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		if path != dropInPath {
			t.Fatalf("unexpected read path: %q", path)
		}
		return []byte(renderServiceIdentityDropIn("app", "www-data")), nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		if cmd != "systemctl" || len(args) != 4 || args[0] != "show" {
			t.Fatalf("unexpected command: %s %#v", cmd, args)
		}
		return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "LoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\n"}, nil
	}

	_, err := (&systemdUnitResource{}).Read(pluginsdk.StateData{"name": "nginx"})
	if err == nil || !strings.Contains(err.Error(), "exists unexpectedly") {
		t.Fatalf("expected unexpected drop-in read error, got %v", err)
	}
}

func TestSystemdUnitReadRejectsModifiedServiceIdentityDropIn(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
	})

	dropInPath := serviceIdentityDropInPath("nginx")
	modified := strings.Replace(renderServiceIdentityDropIn("app", "www-data"), "User=app", "User=manual", 1)
	pluginsdk.FileExists = func(path string) (bool, error) {
		return path == dropInPath, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		if path != dropInPath {
			t.Fatalf("unexpected read path: %q", path)
		}
		return []byte(modified), nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		if cmd != "systemctl" || len(args) != 4 || args[0] != "show" {
			t.Fatalf("unexpected command: %s %#v", cmd, args)
		}
		return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "LoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\n"}, nil
	}

	_, err := (&systemdUnitResource{}).Read(pluginsdk.StateData{
		"name":                         "nginx",
		"service_user":                 "app",
		"service_group":                "www-data",
		"service_identity_dropin_path": dropInPath,
	})
	if err == nil || !strings.Contains(err.Error(), "modified outside Terraform") {
		t.Fatalf("expected modified drop-in read error, got %v", err)
	}
}

func TestSystemdUnitReadRejectsLaterServiceIdentityOverrideWithoutDaemonReload(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origReadDir := pluginsdk.ReadDir
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.ReadDir = origReadDir
	})

	dropInPath := serviceIdentityDropInPath("nginx")
	dropInDir := serviceIdentityDropInDir("nginx")
	overridePath := dropInDir + "/zz-local.conf"
	files := map[string]string{
		dropInPath:   renderServiceIdentityDropIn("app", "www-data"),
		overridePath: "[Service]\nUser=manual\n",
	}
	pluginsdk.FileExists = func(path string) (bool, error) {
		if path == dropInDir {
			return true, nil
		}
		_, ok := files[path]
		return ok, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		if data, ok := files[path]; ok {
			return []byte(data), nil
		}
		return nil, errors.New("missing file")
	}
	pluginsdk.ReadDir = func(path string) ([]pluginsdk.DirEntry, error) {
		if path != dropInDir {
			t.Fatalf("unexpected ReadDir path: %s", path)
		}
		return []pluginsdk.DirEntry{{Name: serviceIdentityDropInFile}, {Name: "zz-local.conf"}}, nil
	}
	var commands []string
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		commands = append(commands, cmd+" "+strings.Join(args, " "))
		if cmd != "systemctl" || len(args) != 4 || args[0] != "show" {
			t.Fatalf("unexpected command: %s %#v", cmd, args)
		}
		return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "LoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\n"}, nil
	}

	_, err := (&systemdUnitResource{}).Read(pluginsdk.StateData{
		"name":                         "nginx",
		"service_user":                 "app",
		"service_group":                "www-data",
		"service_identity_dropin_path": dropInPath,
	})
	if err == nil || !strings.Contains(err.Error(), "higher-priority drop-in") || !strings.Contains(err.Error(), "zz-local.conf") {
		t.Fatalf("expected later drop-in read error, got %v", err)
	}
	if containsCommand(commands, "systemctl daemon-reload") {
		t.Fatalf("Read must not daemon-reload while detecting later drop-ins: %#v", commands)
	}
}

func TestSystemdUnitCreateRejectsLaterServiceIdentityOverrideBeforeWrite(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origFileWrite := pluginsdk.FileWrite
	origDirEnsure := pluginsdk.DirEnsure
	origReadDir := pluginsdk.ReadDir
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.DirEnsure = origDirEnsure
		pluginsdk.ReadDir = origReadDir
	})

	dropInPath := serviceIdentityDropInPath("nginx")
	dropInDir := serviceIdentityDropInDir("nginx")
	overridePath := dropInDir + "/zz-local.conf"
	pluginsdk.FileExists = func(path string) (bool, error) {
		switch path {
		case dropInPath:
			return false, nil
		case dropInDir:
			return true, nil
		default:
			return false, nil
		}
	}
	pluginsdk.ReadDir = func(path string) ([]pluginsdk.DirEntry, error) {
		if path != dropInDir {
			t.Fatalf("unexpected ReadDir path: %s", path)
		}
		return []pluginsdk.DirEntry{{Name: "zz-local.conf"}}, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		if path != overridePath {
			t.Fatalf("unexpected FileRead path: %s", path)
		}
		return []byte("[Service]\nUser=manual\n"), nil
	}
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		t.Fatalf("FileWrite should not be called before later-drop-in conflict is resolved: %s", path)
		return nil
	}
	pluginsdk.DirEnsure = func(path string, mode uint32) error {
		t.Fatalf("DirEnsure should not be called before later-drop-in conflict is resolved: %s", path)
		return nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		t.Fatalf("CmdExec should not be called before later-drop-in conflict is resolved: %s %#v", cmd, args)
		return nil, nil
	}

	_, err := (&systemdUnitResource{}).Create(pluginsdk.StateData{
		"name":         "nginx",
		"service_user": "app",
		"state":        "running",
	})
	if err == nil || !strings.Contains(err.Error(), "higher-priority drop-in") {
		t.Fatalf("expected later drop-in create error, got %v", err)
	}
}

func TestSystemdUnitUpdateRejectsLaterServiceIdentityOverrideBeforeWrite(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origFileWrite := pluginsdk.FileWrite
	origDirEnsure := pluginsdk.DirEnsure
	origReadDir := pluginsdk.ReadDir
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.DirEnsure = origDirEnsure
		pluginsdk.ReadDir = origReadDir
	})

	dropInPath := serviceIdentityDropInPath("nginx")
	dropInDir := serviceIdentityDropInDir("nginx")
	overridePath := dropInDir + "/zz-local.conf"
	pluginsdk.FileExists = func(path string) (bool, error) {
		switch path {
		case dropInPath, dropInDir:
			return true, nil
		default:
			return false, nil
		}
	}
	pluginsdk.ReadDir = func(path string) ([]pluginsdk.DirEntry, error) {
		if path != dropInDir {
			t.Fatalf("unexpected ReadDir path: %s", path)
		}
		return []pluginsdk.DirEntry{{Name: serviceIdentityDropInFile}, {Name: "zz-local.conf"}}, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		switch path {
		case dropInPath:
			return []byte(renderServiceIdentityDropIn("app", "www-data")), nil
		case overridePath:
			return []byte("[Service]\nUser=manual\n"), nil
		default:
			t.Fatalf("unexpected FileRead path: %s", path)
			return nil, nil
		}
	}
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		t.Fatalf("FileWrite should not be called before later-drop-in conflict is resolved: %s", path)
		return nil
	}
	pluginsdk.DirEnsure = func(path string, mode uint32) error {
		t.Fatalf("DirEnsure should not be called before later-drop-in conflict is resolved: %s", path)
		return nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		t.Fatalf("CmdExec should not be called before later-drop-in conflict is resolved: %s %#v", cmd, args)
		return nil, nil
	}

	_, err := (&systemdUnitResource{}).Update(
		pluginsdk.StateData{
			"name":                         "nginx",
			"service_user":                 "app",
			"service_group":                "www-data",
			"service_identity_dropin_path": dropInPath,
		},
		pluginsdk.StateData{
			"name":          "nginx",
			"service_user":  "newapp",
			"service_group": "www-data",
		},
	)
	if err == nil || !strings.Contains(err.Error(), "higher-priority drop-in") {
		t.Fatalf("expected later drop-in update error, got %v", err)
	}
}

func TestSystemdUnitUpdateRestoresPreviousServiceIdentityDropInOnEffectiveMismatch(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origFileWrite := pluginsdk.FileWrite
	origDirEnsure := pluginsdk.DirEnsure
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.DirEnsure = origDirEnsure
	})

	dropInPath := serviceIdentityDropInPath("nginx")
	previous := renderServiceIdentityDropIn("app", "www-data")
	files := map[string]string{dropInPath: previous}
	pluginsdk.DirEnsure = func(string, uint32) error { return nil }
	pluginsdk.FileExists = func(path string) (bool, error) {
		_, ok := files[path]
		return ok, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		if data, ok := files[path]; ok {
			return []byte(data), nil
		}
		return nil, errors.New("missing file")
	}
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		files[path] = string(data)
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
		case len(args) == 4 && args[0] == "show" && args[2] == "User,Group,DropInPaths":
			return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "User=override\nGroup=www-data\nDropInPaths=/etc/systemd/system/nginx.service.d/zz-local.conf\n"}, nil
		default:
			t.Fatalf("unexpected systemctl args: %#v", args)
			return nil, nil
		}
	}

	_, err := (&systemdUnitResource{}).Update(
		pluginsdk.StateData{
			"name":                         "nginx",
			"service_user":                 "app",
			"service_group":                "www-data",
			"service_identity_dropin_path": dropInPath,
			"state":                        "running",
		},
		pluginsdk.StateData{
			"name":          "nginx",
			"service_user":  "newapp",
			"service_group": "www-data",
			"state":         "running",
		},
	)
	if err == nil || !strings.Contains(err.Error(), "systemd resolved service identity") {
		t.Fatalf("expected effective identity mismatch error, got %v", err)
	}
	if got := files[dropInPath]; got != previous {
		t.Fatalf("expected rollback to restore previous drop-in content:\nwant %q\n got %q", previous, got)
	}
	if containsCommand(commands, "systemctl restart nginx.service") || containsCommand(commands, "systemctl start nginx.service") {
		t.Fatalf("service should not be restarted after identity mismatch: %#v", commands)
	}
	if got := countCommand(commands, "systemctl daemon-reload"); got != 2 {
		t.Fatalf("daemon-reload count = %d, want 2; commands %#v", got, commands)
	}
}

func TestSystemdUnitUpdateRejectsModifiedServiceIdentityDropInBeforeDelete(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origFileDelete := pluginsdk.FileDelete
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileDelete = origFileDelete
	})

	dropInPath := serviceIdentityDropInPath("nginx")
	modified := strings.Replace(renderServiceIdentityDropIn("app", "www-data"), "Group=www-data", "Group=manual", 1)
	pluginsdk.FileExists = func(path string) (bool, error) {
		return path == dropInPath, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		if path != dropInPath {
			t.Fatalf("unexpected read path: %s", path)
		}
		return []byte(modified), nil
	}
	pluginsdk.FileDelete = func(path string) error {
		t.Fatalf("FileDelete should not be called for modified drop-in: %s", path)
		return nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		t.Fatalf("CmdExec should not be called for modified drop-in: %s %#v", cmd, args)
		return nil, nil
	}

	_, err := (&systemdUnitResource{}).Update(
		pluginsdk.StateData{
			"name":                         "nginx",
			"service_user":                 "app",
			"service_group":                "www-data",
			"service_identity_dropin_path": dropInPath,
			"state":                        "running",
		},
		pluginsdk.StateData{"name": "nginx", "state": "running"},
	)
	if err == nil || !strings.Contains(err.Error(), "modified outside Terraform") {
		t.Fatalf("expected modified drop-in update error, got %v", err)
	}
}

func TestValidateEffectiveServiceIdentityIgnoresUnspecifiedGroup(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
	})

	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		if cmd != "systemctl" || len(args) != 4 || args[0] != "show" || args[2] != "User,Group,DropInPaths" {
			t.Fatalf("unexpected command: %s %#v", cmd, args)
		}
		return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "User=app\nGroup=other\n"}, nil
	}

	if err := validateEffectiveServiceIdentity("nginx.service", pluginsdk.StateData{"service_user": "app"}); err != nil {
		t.Fatalf("validateEffectiveServiceIdentity returned error: %v", err)
	}
}

func TestSystemdUnitUpdateRemovesServiceIdentityDropInAndRestartsActiveService(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origFileDelete := pluginsdk.FileDelete
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileDelete = origFileDelete
	})

	dropInPath := serviceIdentityDropInPath("nginx")
	dropInExists := true
	pluginsdk.FileExists = func(path string) (bool, error) {
		return path == dropInPath && dropInExists, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		if path != dropInPath || !dropInExists {
			return nil, errors.New("missing file")
		}
		return []byte(renderServiceIdentityDropIn("app", "www-data")), nil
	}
	var deletedPath string
	pluginsdk.FileDelete = func(path string) error {
		deletedPath = path
		dropInExists = false
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
		case len(args) == 2 && args[0] == "restart" && args[1] == "nginx.service":
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		case len(args) == 4 && args[0] == "show":
			return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "LoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\n"}, nil
		default:
			t.Fatalf("unexpected systemctl args: %#v", args)
			return nil, nil
		}
	}

	state, err := (&systemdUnitResource{}).Update(
		pluginsdk.StateData{
			"name":                         "nginx",
			"service_user":                 "app",
			"service_group":                "www-data",
			"service_identity_dropin_path": dropInPath,
			"state":                        "running",
		},
		pluginsdk.StateData{"name": "nginx", "state": "running"},
	)
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if deletedPath != dropInPath {
		t.Fatalf("deleted path = %q, want %q", deletedPath, dropInPath)
	}
	if state.GetString("service_user") != "" || state.GetString("service_group") != "" || state.GetString("service_identity_dropin_path") != "" {
		t.Fatalf("expected service identity to be absent from state, got %#v", state)
	}
	for _, want := range []string{
		"systemctl daemon-reload",
		"systemctl restart nginx.service",
	} {
		if !containsCommand(commands, want) {
			t.Fatalf("expected command %q, got %#v", want, commands)
		}
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

func TestSystemdUnitDeleteRemovesServiceIdentityDropIn(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origFileDelete := pluginsdk.FileDelete
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileDelete = origFileDelete
	})

	dropInPath := serviceIdentityDropInPath("nginx")
	pluginsdk.FileExists = func(path string) (bool, error) {
		return path == dropInPath, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		if path != dropInPath {
			t.Fatalf("unexpected read path: %s", path)
		}
		return []byte(renderServiceIdentityDropIn("app", "")), nil
	}
	var deletedPath string
	pluginsdk.FileDelete = func(path string) error {
		deletedPath = path
		return nil
	}
	var commands []string
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		commands = append(commands, cmd+" "+strings.Join(args, " "))
		if cmd != "systemctl" {
			t.Fatalf("unexpected command: %s %#v", cmd, args)
		}
		return &pluginsdk.CmdResult{ExitCode: 0}, nil
	}

	err := (&systemdUnitResource{}).Delete(pluginsdk.StateData{
		"name":                         "nginx",
		"service_user":                 "app",
		"service_identity_dropin_path": dropInPath,
	})
	if err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	wantCommands := []string{
		"systemctl stop nginx.service",
		"systemctl disable nginx.service",
		"systemctl daemon-reload",
	}
	if !reflect.DeepEqual(commands, wantCommands) {
		t.Fatalf("unexpected commands:\nwant %#v\n got %#v", wantCommands, commands)
	}
	if deletedPath != dropInPath {
		t.Fatalf("deleted path = %q, want %q", deletedPath, dropInPath)
	}
}

func TestSystemdUnitDeleteReturnsServiceIdentityDropInDeleteError(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origFileDelete := pluginsdk.FileDelete
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileDelete = origFileDelete
	})

	dropInPath := serviceIdentityDropInPath("nginx")
	pluginsdk.FileExists = func(path string) (bool, error) {
		return path == dropInPath, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		if path != dropInPath {
			t.Fatalf("unexpected read path: %s", path)
		}
		return []byte(renderServiceIdentityDropIn("app", "")), nil
	}
	pluginsdk.FileDelete = func(path string) error {
		if path != dropInPath {
			t.Fatalf("unexpected delete path: %s", path)
		}
		return errors.New("permission denied")
	}
	var commands []string
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		commands = append(commands, cmd+" "+strings.Join(args, " "))
		return &pluginsdk.CmdResult{ExitCode: 0}, nil
	}

	err := (&systemdUnitResource{}).Delete(pluginsdk.StateData{
		"name":                         "nginx",
		"service_user":                 "app",
		"service_identity_dropin_path": dropInPath,
	})
	if err == nil || !strings.Contains(err.Error(), "delete service identity drop-in") {
		t.Fatalf("expected drop-in delete error, got %v", err)
	}
	if containsCommand(commands, "systemctl daemon-reload") {
		t.Fatalf("daemon-reload should not run after drop-in delete failure: %#v", commands)
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

func containsCommand(commands []string, want string) bool {
	for _, command := range commands {
		if command == want {
			return true
		}
	}
	return false
}

func countCommand(commands []string, want string) int {
	count := 0
	for _, command := range commands {
		if command == want {
			count++
		}
	}
	return count
}
