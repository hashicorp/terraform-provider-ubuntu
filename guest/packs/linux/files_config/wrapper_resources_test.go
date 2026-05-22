package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
	linuxfilescontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/linuxfiles"
	linuxnetworkcontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/linuxnetwork"
)

func TestWrapperSchemasMatchContracts(t *testing.T) {
	tests := []struct {
		name string
		got  pluginsdk.Schema
		want pluginsdk.Schema
	}{
		{name: "kernel modules", got: (&kernelModulesResource{}).Schema(), want: linuxfilescontract.KernelModulesResourceSchema()},
		{name: "swap", got: (&swapResource{}).Schema(), want: linuxfilescontract.SwapResourceSchema()},
		{name: "symlink", got: (&symlinkResource{}).Schema(), want: linuxfilescontract.SymlinkResourceSchema()},
		{name: "hosts entry", got: (&hostsEntryResource{}).Schema(), want: linuxfilescontract.HostsEntryResourceSchema()},
		{name: "network stack", got: (&networkStackResource{}).Schema(), want: linuxnetworkcontract.NetworkStackResourceSchema()},
		{name: "sysctl entry", got: (&sysctlEntryResource{}).Schema(), want: linuxfilescontract.SysctlEntryResourceSchema()},
		{name: "fstab entry", got: (&fstabEntryResource{}).Schema(), want: linuxfilescontract.FstabEntryResourceSchema()},
		{name: "sshd config", got: (&sshdConfigResource{}).Schema(), want: linuxfilescontract.SSHDConfigResourceSchema()},
	}

	for _, tc := range tests {
		if !reflect.DeepEqual(tc.got, tc.want) {
			t.Fatalf("%s schema mismatch", tc.name)
		}
	}
}

func TestWrapperValidatePaths(t *testing.T) {
	withFilesConfigPluginStubs(t)
	pluginsdk.HasCommand = func(name string) (bool, error) {
		return name == "validator", nil
	}

	tests := []struct {
		name     string
		validate func() error
		wantErr  string
	}{
		{
			name: "kernel modules rejects relative path",
			validate: func() error {
				return (&kernelModulesResource{}).Validate(pluginsdk.StateData{"path": "modules.conf", "modules": []string{"overlay"}})
			},
			wantErr: "absolute path",
		},
		{
			name: "kernel modules accepts normalized config",
			validate: func() error {
				return (&kernelModulesResource{}).Validate(pluginsdk.StateData{"path": "/etc/modules-load.d/demo.conf", "modules": []string{"overlay", "overlay"}})
			},
		},
		{
			name: "swap requires enabled flag",
			validate: func() error {
				return (&swapResource{}).Validate(pluginsdk.StateData{})
			},
			wantErr: "enabled must be set",
		},
		{
			name: "swap accepts explicit enabled",
			validate: func() error {
				return (&swapResource{}).Validate(pluginsdk.StateData{"enabled": true})
			},
		},
		{
			name: "file rejects relative path",
			validate: func() error {
				return (&fileResource{}).Validate(pluginsdk.StateData{"path": "demo.conf", "content": "demo"})
			},
			wantErr: "absolute path",
		},
		{
			name: "file validates host command aware config",
			validate: func() error {
				return (&fileResource{}).Validate(pluginsdk.StateData{
					"path":    "/etc/demo.conf",
					"content": "demo",
					"validation": map[string]interface{}{
						"argv": []interface{}{"validator", "--check"},
					},
				})
			},
		},
		{
			name: "symlink requires target",
			validate: func() error {
				return (&symlinkResource{}).Validate(pluginsdk.StateData{"path": "/etc/demo"})
			},
			wantErr: "target must not be empty",
		},
		{
			name: "symlink accepts absolute path and target",
			validate: func() error {
				return (&symlinkResource{}).Validate(pluginsdk.StateData{"path": "/etc/demo", "target": "/opt/demo"})
			},
		},
		{
			name: "hosts entry rejects invalid ip",
			validate: func() error {
				return (&hostsEntryResource{}).Validate(pluginsdk.StateData{"ip": "bad", "hostname": "app.internal"})
			},
			wantErr: "invalid IP address",
		},
		{
			name: "hosts entry accepts valid host tokens",
			validate: func() error {
				return (&hostsEntryResource{}).Validate(pluginsdk.StateData{"ip": "10.0.0.10", "hostname": "app.internal", "aliases": []string{"api.internal"}})
			},
		},
		{
			name: "network stack validate is a no-op",
			validate: func() error {
				return (&networkStackResource{}).Validate(nil)
			},
		},
		{
			name: "sysctl requires key",
			validate: func() error {
				return (&sysctlEntryResource{}).Validate(pluginsdk.StateData{"value": "1"})
			},
			wantErr: "key must not be empty",
		},
		{
			name: "sysctl accepts key and value",
			validate: func() error {
				return (&sysctlEntryResource{}).Validate(pluginsdk.StateData{"key": "net.ipv4.ip_forward", "value": "1"})
			},
		},
		{
			name: "fstab rejects relative mount",
			validate: func() error {
				return (&fstabEntryResource{}).Validate(pluginsdk.StateData{"device": "/dev/sda1", "mount": "var", "fstype": "ext4"})
			},
			wantErr: "mount must be an absolute path",
		},
		{
			name: "fstab accepts complete config",
			validate: func() error {
				return (&fstabEntryResource{}).Validate(pluginsdk.StateData{"device": "/dev/sda1", "mount": "/var", "fstype": "ext4", "dump": 0, "passno": 2})
			},
		},
		{
			name: "sshd rejects invalid port",
			validate: func() error {
				return (&sshdConfigResource{}).Validate(pluginsdk.StateData{"port": 70000})
			},
			wantErr: "port must be between 1 and 65535",
		},
		{
			name: "sshd accepts valid managed directives",
			validate: func() error {
				return (&sshdConfigResource{}).Validate(pluginsdk.StateData{"port": 2222, "password_authentication": "no"})
			},
		},
	}

	for _, tc := range tests {
		err := tc.validate()
		if tc.wantErr == "" {
			if err != nil {
				t.Fatalf("%s: error = %v, want nil", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("%s: error = %v, want substring %q", tc.name, err, tc.wantErr)
		}
	}
}

func TestWrapperImportPaths(t *testing.T) {
	t.Run("kernel modules import", func(t *testing.T) {
		withFilesConfigPluginStubs(t)
		if _, err := (&kernelModulesResource{}).ImportState("relative.conf"); err == nil || !strings.Contains(err.Error(), "absolute file path") {
			t.Fatalf("unexpected kernel modules import error: %v", err)
		}
		pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
			return &pluginsdk.FileStat{Path: path, Owner: "root", Group: "root", Mode: 0o644}, nil
		}
		pluginsdk.FileRead = func(path string) ([]byte, error) {
			return []byte("overlay\nbr_netfilter\n"), nil
		}
		state, err := (&kernelModulesResource{}).ImportState("/etc/modules-load.d/demo.conf")
		if err != nil {
			t.Fatalf("ImportState() error = %v, want nil", err)
		}
		if got := state.GetStringList("modules"); !reflect.DeepEqual(got, []string{"overlay", "br_netfilter"}) {
			t.Fatalf("unexpected kernel modules state: %#v", state)
		}
	})

	t.Run("swap import", func(t *testing.T) {
		withFilesConfigPluginStubs(t)
		if _, err := (&swapResource{}).ImportState("other"); err == nil || !strings.Contains(err.Error(), "must be empty") {
			t.Fatalf("unexpected swap import error: %v", err)
		}
		pluginsdk.FileRead = func(path string) ([]byte, error) {
			switch path {
			case swapInfoPath:
				return []byte("Filename\tType\tSize\tUsed\tPriority\n"), nil
			case fstabConfigPath:
				return []byte("UUID=root / ext4 defaults 0 1\n"), nil
			default:
				t.Fatalf("unexpected read path %q", path)
				return nil, nil
			}
		}
		state, err := (&swapResource{}).ImportState("system")
		if err != nil {
			t.Fatalf("ImportState() error = %v, want nil", err)
		}
		if state.GetString("id") != "system" || state.GetBool("enabled") {
			t.Fatalf("unexpected swap import state: %#v", state)
		}
	})

	t.Run("network stack import", func(t *testing.T) {
		withFilesConfigPluginStubs(t)
		if _, err := (&networkStackResource{}).ImportState(""); err == nil || !strings.Contains(err.Error(), "import ID must be") {
			t.Fatalf("unexpected network stack import error: %v", err)
		}
		pluginsdk.FileRead = func(path string) ([]byte, error) {
			if path != networkStackConfigPath {
				t.Fatalf("unexpected read path %q", path)
			}
			return []byte("net.ipv4.ip_forward=1\nnet.ipv6.conf.all.forwarding=0\nnet.ipv6.conf.default.forwarding=0\nnet.bridge.bridge-nf-call-iptables=1\nnet.bridge.bridge-nf-call-ip6tables=0\n"), nil
		}
		state, err := (&networkStackResource{}).ImportState(networkStackConfigPath)
		if err != nil {
			t.Fatalf("ImportState() error = %v, want nil", err)
		}
		if state.GetString("id") != networkStackID() || !state.GetBool("ipv4_forwarding") {
			t.Fatalf("unexpected network stack import state: %#v", state)
		}
	})

	t.Run("hosts entry import", func(t *testing.T) {
		withFilesConfigPluginStubs(t)
		if _, err := (&hostsEntryResource{}).ImportState("bad-id"); err == nil || !strings.Contains(err.Error(), "ip/hostname") {
			t.Fatalf("unexpected hosts import error: %v", err)
		}
		pluginsdk.FileRead = func(path string) ([]byte, error) {
			if path != "/etc/hosts" {
				t.Fatalf("unexpected read path %q", path)
			}
			return []byte("10.0.0.10 app.internal api.internal\n"), nil
		}
		state, err := (&hostsEntryResource{}).ImportState("10.0.0.10/app.internal")
		if err != nil {
			t.Fatalf("ImportState() error = %v, want nil", err)
		}
		if state.GetString("id") != "10.0.0.10/app.internal" || state.GetString("hostname") != "app.internal" {
			t.Fatalf("unexpected hosts import state: %#v", state)
		}
	})

	t.Run("sysctl import", func(t *testing.T) {
		withFilesConfigPluginStubs(t)
		if _, err := (&sysctlEntryResource{}).ImportState(" "); err == nil || !strings.Contains(err.Error(), "sysctl key") {
			t.Fatalf("unexpected sysctl import error: %v", err)
		}
		pluginsdk.FileRead = func(path string) ([]byte, error) {
			if path != sysctlConfigPath {
				t.Fatalf("unexpected read path %q", path)
			}
			return []byte("net.ipv4.ip_forward=1\n"), nil
		}
		state, err := (&sysctlEntryResource{}).ImportState("net.ipv4.ip_forward")
		if err != nil {
			t.Fatalf("ImportState() error = %v, want nil", err)
		}
		if state.GetString("key") != "net.ipv4.ip_forward" || state.GetString("value") != "1" {
			t.Fatalf("unexpected sysctl import state: %#v", state)
		}
	})

	t.Run("fstab import", func(t *testing.T) {
		withFilesConfigPluginStubs(t)
		if _, err := (&fstabEntryResource{}).ImportState("var"); err == nil || !strings.Contains(err.Error(), "absolute mount path") {
			t.Fatalf("unexpected fstab import error: %v", err)
		}
		pluginsdk.FileRead = func(path string) ([]byte, error) {
			if path != fstabConfigPath {
				t.Fatalf("unexpected read path %q", path)
			}
			return []byte("/dev/sda1 /var ext4 defaults 0 2\n"), nil
		}
		state, err := (&fstabEntryResource{}).ImportState("/var")
		if err != nil {
			t.Fatalf("ImportState() error = %v, want nil", err)
		}
		if state.GetString("mount") != "/var" || state.GetString("device") != "/dev/sda1" {
			t.Fatalf("unexpected fstab import state: %#v", state)
		}
	})

	t.Run("sshd config import", func(t *testing.T) {
		withFilesConfigPluginStubs(t)
		if _, err := (&sshdConfigResource{}).ImportState("other"); err == nil || !strings.Contains(err.Error(), `must be "sshd_config"`) {
			t.Fatalf("unexpected sshd import error: %v", err)
		}
		pluginsdk.FileRead = func(path string) ([]byte, error) {
			if path != sshdConfigPath {
				t.Fatalf("unexpected read path %q", path)
			}
			return []byte("Port 2222\nPasswordAuthentication no\n"), nil
		}
		state, err := (&sshdConfigResource{}).ImportState("sshd_config")
		if err != nil {
			t.Fatalf("ImportState() error = %v, want nil", err)
		}
		if state.GetInt("port") != 2222 || state.GetString("password_authentication") != "no" {
			t.Fatalf("unexpected sshd import state: %#v", state)
		}
	})
}

func TestWrapperCreateAndDeletePaths(t *testing.T) {
	t.Run("swap create delegates through apply path", func(t *testing.T) {
		withFilesConfigPluginStubs(t)
		pluginsdk.FileLock = func(path string) (uint32, error) {
			if path != fstabConfigPath {
				t.Fatalf("unexpected lock path %q", path)
			}
			return 1, nil
		}
		pluginsdk.FileUnlock = func(handle uint32) error {
			if handle != 1 {
				t.Fatalf("unexpected unlock handle %d", handle)
			}
			return nil
		}
		pluginsdk.FileRead = func(path string) ([]byte, error) {
			switch path {
			case swapInfoPath:
				return []byte("Filename\tType\tSize\tUsed\tPriority\n/swapfile file 1024 0 -2\n"), nil
			case fstabConfigPath:
				return []byte("/swapfile none swap sw 0 0\nUUID=root / ext4 defaults 0 1\n"), nil
			default:
				t.Fatalf("unexpected read path %q", path)
				return nil, nil
			}
		}
		pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
			if path != fstabConfigPath || !strings.Contains(string(data), swapDisabledCommentPrefix) {
				t.Fatalf("unexpected swap fstab write path=%q data=%q", path, string(data))
			}
			return nil
		}
		pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
			if cmd != "swapoff" || !reflect.DeepEqual(args, []string{"-a"}) {
				t.Fatalf("unexpected command %q %#v", cmd, args)
			}
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		}

		state, err := (&swapResource{}).Create(pluginsdk.StateData{"enabled": false})
		if err != nil {
			t.Fatalf("Create() error = %v, want nil", err)
		}
		if state.GetBool("enabled") || state.GetString("id") != "system" {
			t.Fatalf("unexpected swap create state: %#v", state)
		}
	})

	t.Run("network stack create requires import when file exists", func(t *testing.T) {
		withFilesConfigPluginStubs(t)
		pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
			if path != networkStackConfigPath {
				t.Fatalf("unexpected stat path %q", path)
			}
			return &pluginsdk.FileStat{Path: path, Mode: 0o644}, nil
		}

		_, err := (&networkStackResource{}).Create(pluginsdk.StateData{"ipv4_forwarding": true})
		if err == nil || !strings.Contains(err.Error(), "import it before managing with terraform") {
			t.Fatalf("unexpected network stack create error: %v", err)
		}
	})

	t.Run("network stack delete handles missing and existing files", func(t *testing.T) {
		withFilesConfigPluginStubs(t)
		pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
			return nil, assertErr("stat " + path + ": no such file or directory")
		}
		if err := (&networkStackResource{}).Delete(nil); err != nil {
			t.Fatalf("Delete() missing file error = %v, want nil", err)
		}

		deleted := ""
		pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
			return &pluginsdk.FileStat{Path: path, Mode: 0o644}, nil
		}
		pluginsdk.FileDelete = func(path string) error {
			deleted = path
			return nil
		}
		if err := (&networkStackResource{}).Delete(nil); err != nil {
			t.Fatalf("Delete() existing file error = %v, want nil", err)
		}
		if deleted != networkStackConfigPath {
			t.Fatalf("deleted path = %q, want %q", deleted, networkStackConfigPath)
		}
	})

	t.Run("hosts, sysctl, and fstab delete rewrite managed files", func(t *testing.T) {
		withFilesConfigPluginStubs(t)
		pluginsdk.FileLock = func(path string) (uint32, error) { return 1, nil }
		pluginsdk.FileUnlock = func(handle uint32) error { return nil }

		writes := map[string]string{}
		pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
			writes[path] = string(data)
			return nil
		}

		pluginsdk.FileRead = func(path string) ([]byte, error) {
			switch path {
			case "/etc/hosts":
				return []byte("10.0.0.10 app.internal\n10.0.0.11 other.internal\n"), nil
			case sysctlConfigPath:
				return []byte("net.ipv4.ip_forward=1\nnet.ipv4.conf.all.rp_filter=1\n"), nil
			case fstabConfigPath:
				return []byte("/dev/sda1 / ext4 defaults 0 1\n/dev/sdb1 /var ext4 defaults 0 2\n"), nil
			default:
				t.Fatalf("unexpected read path %q", path)
				return nil, nil
			}
		}

		if err := (&hostsEntryResource{}).Delete(pluginsdk.StateData{"ip": "10.0.0.10", "hostname": "app.internal"}); err != nil {
			t.Fatalf("hosts delete error = %v", err)
		}
		if strings.Contains(writes["/etc/hosts"], "app.internal") || !strings.Contains(writes["/etc/hosts"], "other.internal") {
			t.Fatalf("unexpected /etc/hosts contents after delete: %q", writes["/etc/hosts"])
		}

		if err := (&sysctlEntryResource{}).Delete(pluginsdk.StateData{"key": "net.ipv4.ip_forward"}); err != nil {
			t.Fatalf("sysctl delete error = %v", err)
		}
		if strings.Contains(writes[sysctlConfigPath], "net.ipv4.ip_forward") || !strings.Contains(writes[sysctlConfigPath], "rp_filter") {
			t.Fatalf("unexpected sysctl contents after delete: %q", writes[sysctlConfigPath])
		}

		if err := (&fstabEntryResource{}).Delete(pluginsdk.StateData{"mount": "/"}); err != nil {
			t.Fatalf("fstab delete error = %v", err)
		}
		if strings.Contains(writes[fstabConfigPath], " / ") || !strings.Contains(writes[fstabConfigPath], "/var") {
			t.Fatalf("unexpected fstab contents after delete: %q", writes[fstabConfigPath])
		}
	})
}

func TestAdditionalWrapperPaths(t *testing.T) {
	t.Run("kernel modules create and delete", func(t *testing.T) {
		withFilesConfigPluginStubs(t)
		pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
			switch path {
			case "/etc/modules-load.d":
				return nil, assertErr("stat /etc/modules-load.d: no such file or directory")
			case "/etc/modules-load.d/demo.conf":
				return &pluginsdk.FileStat{Path: path, Mode: 0o644}, nil
			default:
				t.Fatalf("unexpected stat path %q", path)
				return nil, nil
			}
		}
		pluginsdk.DirEnsure = func(path string, mode uint32) error {
			if path != "/etc/modules-load.d" || mode != 0o755 {
				t.Fatalf("unexpected DirEnsure(%q, %#o)", path, mode)
			}
			return nil
		}
		pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
			if path != "/etc/modules-load.d/demo.conf" || string(data) != "overlay\n" || mode != 0o644 {
				t.Fatalf("unexpected kernel module write path=%q mode=%#o data=%q", path, mode, string(data))
			}
			return nil
		}
		pluginsdk.FileChown = func(path string, owner, group string) error {
			if path != "/etc/modules-load.d/demo.conf" || owner != "root" || group != "root" {
				t.Fatalf("unexpected FileChown(%q, %q, %q)", path, owner, group)
			}
			return nil
		}
		pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
			if cmd != "modprobe" || !reflect.DeepEqual(args, []string{"overlay"}) {
				t.Fatalf("unexpected CmdExec(%q, %#v)", cmd, args)
			}
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		}

		state, err := (&kernelModulesResource{}).Create(pluginsdk.StateData{"path": "/etc/modules-load.d/demo.conf", "modules": []string{"overlay"}})
		if err != nil {
			t.Fatalf("Create() error = %v, want nil", err)
		}
		if state.GetString("path") != "/etc/modules-load.d/demo.conf" {
			t.Fatalf("unexpected kernel module state: %#v", state)
		}

		deleted := ""
		pluginsdk.FileDelete = func(path string) error {
			deleted = path
			return nil
		}
		if err := (&kernelModulesResource{}).Delete(pluginsdk.StateData{"path": "/etc/modules-load.d/demo.conf"}); err != nil {
			t.Fatalf("Delete() error = %v, want nil", err)
		}
		if deleted != "/etc/modules-load.d/demo.conf" {
			t.Fatalf("deleted path = %q, want kernel module config path", deleted)
		}
	})

	t.Run("file import and delete", func(t *testing.T) {
		withFilesConfigPluginStubs(t)
		pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
			return &pluginsdk.FileStat{Path: path, Mode: 0o640, Owner: "app", Group: "app", Digest: "blake3:abc"}, nil
		}
		state, err := (&fileResource{}).ImportState("/etc/app.conf")
		if err != nil {
			t.Fatalf("ImportState() error = %v, want nil", err)
		}
		if state.GetString("path") != "/etc/app.conf" || state.GetString("digest") != "blake3:abc" {
			t.Fatalf("unexpected file import state: %#v", state)
		}

		deleted := ""
		pluginsdk.FileDelete = func(path string) error {
			deleted = path
			return nil
		}
		if err := (&fileResource{}).Delete(pluginsdk.StateData{"path": "/etc/app.conf"}); err != nil {
			t.Fatalf("Delete() error = %v, want nil", err)
		}
		if deleted != "/etc/app.conf" {
			t.Fatalf("deleted path = %q, want /etc/app.conf", deleted)
		}
	})

	t.Run("symlink import and delete", func(t *testing.T) {
		withFilesConfigPluginStubs(t)
		pluginsdk.FileReadlink = func(path string) (string, error) {
			if path != "/etc/demo-link" {
				t.Fatalf("unexpected FileReadlink(%q)", path)
			}
			return "/opt/demo", nil
		}
		state, err := (&symlinkResource{}).ImportState("/etc/demo-link")
		if err != nil {
			t.Fatalf("ImportState() error = %v, want nil", err)
		}
		if state.GetString("target") != "/opt/demo" {
			t.Fatalf("unexpected symlink import state: %#v", state)
		}

		deleted := ""
		pluginsdk.FileDelete = func(path string) error {
			deleted = path
			return nil
		}
		if err := (&symlinkResource{}).Delete(pluginsdk.StateData{"path": "/etc/demo-link"}); err != nil {
			t.Fatalf("Delete() error = %v, want nil", err)
		}
		if deleted != "/etc/demo-link" {
			t.Fatalf("deleted path = %q, want /etc/demo-link", deleted)
		}
	})

	t.Run("hosts entry update rewrites managed entry", func(t *testing.T) {
		withFilesConfigPluginStubs(t)
		pluginsdk.FileLock = func(path string) (uint32, error) { return 1, nil }
		pluginsdk.FileUnlock = func(handle uint32) error { return nil }
		pluginsdk.FileRead = func(path string) ([]byte, error) {
			if path != "/etc/hosts" {
				t.Fatalf("unexpected read path %q", path)
			}
			return []byte("10.0.0.10 app.internal old.internal\n"), nil
		}
		written := ""
		pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
			if path != "/etc/hosts" {
				t.Fatalf("unexpected write path %q", path)
			}
			written = string(data)
			return nil
		}

		state, err := (&hostsEntryResource{}).Update(
			pluginsdk.StateData{"ip": "10.0.0.10", "hostname": "app.internal"},
			pluginsdk.StateData{"aliases": []string{"api.internal"}, "comment": "managed by tf-linux-provider"},
		)
		if err != nil {
			t.Fatalf("Update() error = %v, want nil", err)
		}
		if !strings.Contains(written, "api.internal") || !strings.Contains(written, "managed by tf-linux-provider") {
			t.Fatalf("unexpected /etc/hosts contents after update: %q", written)
		}
		if got := state.GetStringList("aliases"); !reflect.DeepEqual(got, []string{"api.internal"}) {
			t.Fatalf("unexpected hosts update state: %#v", state)
		}
	})
}

func withFilesConfigPluginStubs(t *testing.T) {
	t.Helper()
	oldHostHasCommand := pluginsdk.HasCommand
	oldFileRead := pluginsdk.FileRead
	oldFileWrite := pluginsdk.FileWrite
	oldFileStat := pluginsdk.FileStat_
	oldFileDelete := pluginsdk.FileDelete
	oldFileReadlink := pluginsdk.FileReadlink
	oldFileExists := pluginsdk.FileExists
	oldDirEnsure := pluginsdk.DirEnsure
	oldFileChown := pluginsdk.FileChown
	oldFileLock := pluginsdk.FileLock
	oldFileUnlock := pluginsdk.FileUnlock
	oldCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.HasCommand = oldHostHasCommand
		pluginsdk.FileRead = oldFileRead
		pluginsdk.FileWrite = oldFileWrite
		pluginsdk.FileStat_ = oldFileStat
		pluginsdk.FileDelete = oldFileDelete
		pluginsdk.FileReadlink = oldFileReadlink
		pluginsdk.FileExists = oldFileExists
		pluginsdk.DirEnsure = oldDirEnsure
		pluginsdk.FileChown = oldFileChown
		pluginsdk.FileLock = oldFileLock
		pluginsdk.FileUnlock = oldFileUnlock
		pluginsdk.CmdExec = oldCmdExec
	})
	pluginsdk.HasCommand = func(name string) (bool, error) {
		t.Fatalf("unexpected HostHasCommand(%q)", name)
		return false, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		t.Fatalf("unexpected FileRead(%q)", path)
		return nil, nil
	}
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		t.Fatalf("unexpected FileWrite(%q)", path)
		return nil
	}
	pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
		t.Fatalf("unexpected FileStat_(%q)", path)
		return nil, nil
	}
	pluginsdk.FileDelete = func(path string) error {
		t.Fatalf("unexpected FileDelete(%q)", path)
		return nil
	}
	pluginsdk.FileReadlink = func(path string) (string, error) {
		t.Fatalf("unexpected FileReadlink(%q)", path)
		return "", nil
	}
	pluginsdk.FileExists = func(path string) (bool, error) {
		t.Fatalf("unexpected FileExists(%q)", path)
		return false, nil
	}
	pluginsdk.DirEnsure = func(path string, mode uint32) error {
		t.Fatalf("unexpected DirEnsure(%q, %#o)", path, mode)
		return nil
	}
	pluginsdk.FileChown = func(path string, owner, group string) error {
		t.Fatalf("unexpected FileChown(%q, %q, %q)", path, owner, group)
		return nil
	}
	pluginsdk.FileLock = func(path string) (uint32, error) {
		t.Fatalf("unexpected FileLock(%q)", path)
		return 0, nil
	}
	pluginsdk.FileUnlock = func(handle uint32) error {
		t.Fatalf("unexpected FileUnlock(%d)", handle)
		return nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		t.Fatalf("unexpected CmdExec(%q, %#v)", cmd, args)
		return nil, nil
	}
}
