package main

import (
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
)

func TestSystemInfoDataReadUsesHostProfile(t *testing.T) {
	origGetHostProfile := pluginsdk.GetHostProfile
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.GetHostProfile = origGetHostProfile
		pluginsdk.CmdExec = origCmdExec
	})

	pluginsdk.GetHostProfile = func() (*pluginsdk.HostProfile, error) {
		return &pluginsdk.HostProfile{
			Hostname:       "node-1",
			Arch:           "x86_64",
			Kernel:         "Linux",
			KernelVersion:  "6.8.0-41-generic",
			Distro:         "ubuntu",
			DistroFamily:   "debian",
			InitSystem:     "systemd",
			PackageManager: "apt",
			SELinux:        false,
			AppArmor:       true,
		}, nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		t.Fatalf("unexpected command execution: %s %#v", cmd, args)
		return nil, nil
	}

	state, err := (&systemInfoDataSource{}).DataRead(nil)
	if err != nil {
		t.Fatalf("DataRead returned error: %v", err)
	}
	if state.GetString("hostname") != "node-1" {
		t.Fatalf("unexpected hostname: %#v", state)
	}
	if state.GetString("kernel_version") != "6.8.0-41-generic" {
		t.Fatalf("unexpected kernel version: %#v", state)
	}
	if state.GetString("distro") != "ubuntu" || state.GetString("package_manager") != "apt" {
		t.Fatalf("unexpected system info state: %#v", state)
	}
}

func TestParseProcNetDev(t *testing.T) {
	t.Parallel()

	content := "Inter-|   Receive                                                |  Transmit\n face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n    lo: 1 1 0 0 0 0 0 0 1 1 0 0 0 0 0 0\n  eth0: 2 2 0 0 0 0 0 0 2 2 0 0 0 0 0 0\n"
	want := []string{"eth0", "lo"}
	if got := parseProcNetDev(content); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected interfaces: got %v want %v", got, want)
	}
}

func TestParseProcMounts(t *testing.T) {
	t.Parallel()

	content := "/dev/sda1 / ext4 rw,relatime 0 0\ntmpfs /run tmpfs rw,nosuid,nodev 0 0\n"
	mounts, devices, fstypes, options := parseProcMounts(content)

	if !reflect.DeepEqual(mounts, []string{"/", "/run"}) {
		t.Fatalf("unexpected mounts: %v", mounts)
	}
	if devices["/"] != "/dev/sda1" || fstypes["/"] != "ext4" || options["/"] != "rw,relatime" {
		t.Fatalf("unexpected root mount data: devices=%v fstypes=%v options=%v", devices, fstypes, options)
	}
}
