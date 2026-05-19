package main

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/aptkeyring"
)

func TestInstallPackageAptAllowsChangingHeldPackages(t *testing.T) {
	originalCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.CmdExec = originalCmdExec
	})

	var gotCmd string
	var gotArgs []string
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		gotCmd = cmd
		gotArgs = append([]string(nil), args...)
		return &pluginsdk.CmdResult{ExitCode: 0}, nil
	}

	if err := installPackage("apt", "containerd", ""); err != nil {
		t.Fatalf("installPackage returned error: %v", err)
	}

	wantArgs := []string{"DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "--allow-change-held-packages", "containerd"}
	if gotCmd != "env" {
		t.Fatalf("installPackage command = %q, want %q", gotCmd, "env")
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("installPackage args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestRemovePackageAptAllowsChangingHeldPackages(t *testing.T) {
	originalCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.CmdExec = originalCmdExec
	})

	type call struct {
		cmd  string
		args []string
	}
	var calls []call
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		calls = append(calls, call{cmd: cmd, args: append([]string(nil), args...)})
		switch len(calls) {
		case 1:
			return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "ii\t1.7.27-0ubuntu1"}, nil
		case 2:
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		default:
			t.Fatalf("unexpected extra command: %s %#v", cmd, args)
			return nil, nil
		}
	}

	if err := removePackage("apt", "containerd"); err != nil {
		t.Fatalf("removePackage returned error: %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("removePackage issued %d commands, want 2", len(calls))
	}

	wantQueryArgs := []string{"-W", "-f=${db:Status-Abbrev}\t${Version}", "containerd"}
	if calls[0].cmd != "dpkg-query" {
		t.Fatalf("query command = %q, want %q", calls[0].cmd, "dpkg-query")
	}
	if !reflect.DeepEqual(calls[0].args, wantQueryArgs) {
		t.Fatalf("query args = %#v, want %#v", calls[0].args, wantQueryArgs)
	}

	wantPurgeArgs := []string{"DEBIAN_FRONTEND=noninteractive", "apt-get", "purge", "-y", "--allow-change-held-packages", "containerd"}
	if calls[1].cmd != "env" {
		t.Fatalf("purge command = %q, want %q", calls[1].cmd, "env")
	}
	if !reflect.DeepEqual(calls[1].args, wantPurgeArgs) {
		t.Fatalf("purge args = %#v, want %#v", calls[1].args, wantPurgeArgs)
	}
}

func TestPackageSpec(t *testing.T) {
	t.Parallel()

	if got, err := packageSpec("apt", "nginx", "1.2.3"); err != nil || got != "nginx=1.2.3" {
		t.Fatalf("unexpected apt package spec: %q, %v", got, err)
	}
	if got, err := packageSpec("dnf", "nginx", "1.2.3-1"); err != nil || got != "nginx-1.2.3-1" {
		t.Fatalf("unexpected dnf package spec: %q, %v", got, err)
	}
	if _, err := packageSpec("pacman", "nginx", "1.2.3"); err == nil {
		t.Fatal("expected pacman exact-version request to fail")
	}
}

func TestPackageEnsureDefaultsToPresent(t *testing.T) {
	t.Parallel()

	if got := packageEnsure(nil); got != "present" {
		t.Fatalf("unexpected default ensure: got %q want present", got)
	}
	if got := packageEnsure(map[string]interface{}{"ensure": "absent"}); got != "absent" {
		t.Fatalf("unexpected explicit ensure: got %q want absent", got)
	}
}

func TestPackageValidateRequiresRepoKeyringPair(t *testing.T) {
	t.Parallel()

	err := (&packageResource{}).Validate(pluginsdk.StateData{
		"name":              "gpg",
		"repo_keyring_path": "/etc/apt/keyrings/kubernetes-apt-keyring.gpg",
	})
	if err == nil || !strings.Contains(err.Error(), "repo_keyring_path and repo_keyring_url must be set together") {
		t.Fatalf("expected repo keyring pair validation error, got %v", err)
	}
}

func TestPackageLockEnsureDefaultsToPresent(t *testing.T) {
	t.Parallel()

	if got := packageLockEnsure(nil); got != "present" {
		t.Fatalf("unexpected default ensure: got %q want present", got)
	}
	if got := packageLockEnsure(map[string]interface{}{"ensure": "absent"}); got != "absent" {
		t.Fatalf("unexpected explicit ensure: got %q want absent", got)
	}
}

func TestIsTransientAptBusy(t *testing.T) {
	t.Parallel()

	if !isTransientAptBusy("E: Could not get lock /var/lib/dpkg/lock-frontend. It is held by process 1234") {
		t.Fatal("expected dpkg frontend lock error to be treated as transient")
	}
	if isTransientAptBusy("E: Unable to locate package kubeadm") {
		t.Fatal("expected permanent apt failure to bypass retry classification")
	}
}

func TestRunAptCommandWithRetryRetriesTransientFailure(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origLogInfo := pluginsdk.LogInfo
	origAttempts := packageRetryMaxAttempts
	origDelay := packageRetryBaseDelay
	origSleep := packageRetrySleep
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.LogInfo = origLogInfo
		packageRetryMaxAttempts = origAttempts
		packageRetryBaseDelay = origDelay
		packageRetrySleep = origSleep
	})

	packageRetryMaxAttempts = 3
	packageRetryBaseDelay = time.Millisecond
	packageRetrySleep = func(time.Duration) {}
	pluginsdk.LogInfo = func(string) {}

	var calls int
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		calls++
		if calls < 3 {
			return &pluginsdk.CmdResult{
				ExitCode: 100,
				Stderr:   "E: Could not get lock /var/lib/dpkg/lock-frontend. It is held by process 1234",
			}, nil
		}
		return &pluginsdk.CmdResult{ExitCode: 0}, nil
	}

	if err := runAptCommandWithRetry("install package \"kubelet\"", "env", []string{"DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "kubelet"}); err != nil {
		t.Fatalf("expected transient apt contention to recover, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", calls)
	}
}

func TestRunAptCommandWithRetryFailsFastOnPermanentFailure(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origLogInfo := pluginsdk.LogInfo
	origAttempts := packageRetryMaxAttempts
	origDelay := packageRetryBaseDelay
	origSleep := packageRetrySleep
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.LogInfo = origLogInfo
		packageRetryMaxAttempts = origAttempts
		packageRetryBaseDelay = origDelay
		packageRetrySleep = origSleep
	})

	packageRetryMaxAttempts = 3
	packageRetryBaseDelay = time.Millisecond
	packageRetrySleep = func(time.Duration) {}
	pluginsdk.LogInfo = func(string) {}

	var calls int
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		calls++
		return &pluginsdk.CmdResult{
			ExitCode: 100,
			Stderr:   "E: Unable to locate package kubelet",
		}, nil
	}

	err := runAptCommandWithRetry("install package \"kubelet\"", "env", []string{"DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "kubelet"})
	if err == nil {
		t.Fatal("expected permanent apt failure")
	}
	if !strings.Contains(err.Error(), "Unable to locate package kubelet") {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected permanent failure to avoid retry, got %d attempts", calls)
	}
}

func TestEnsurePackageRepoKeyringUsesHostInstaller(t *testing.T) {
	origFetchURL := pluginsdk.FetchURL
	origDirEnsure := pluginsdk.DirEnsure
	origFileWrite := pluginsdk.FileWrite
	t.Cleanup(func() {
		pluginsdk.FetchURL = origFetchURL
		pluginsdk.DirEnsure = origDirEnsure
		pluginsdk.FileWrite = origFileWrite
	})

	var installedURL, installedPath string
	pluginsdk.FetchURL = func(url string) ([]byte, error) {
		installedURL = url
		return []byte("raw-key"), nil
	}
	pluginsdk.DirEnsure = func(string, uint32) error { return nil }
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		installedPath = path
		return nil
	}

	err := ensurePackageRepoKeyring("apt", pluginsdk.StateData{
		"repo_keyring_path": "/etc/apt/keyrings/kubernetes-apt-keyring.gpg",
		"repo_keyring_url":  "https://pkgs.k8s.io/core:/stable:/v1.31/deb/Release.key",
	})
	if err != nil {
		t.Fatalf("ensurePackageRepoKeyring returned error: %v", err)
	}
	if installedURL != "https://pkgs.k8s.io/core:/stable:/v1.31/deb/Release.key" {
		t.Fatalf("unexpected installed key url: %q", installedURL)
	}
	if installedPath != "/etc/apt/keyrings/kubernetes-apt-keyring.gpg" {
		t.Fatalf("unexpected installed key path: %q", installedPath)
	}
}

func TestRemovePackageRepoKeyringSkipsReferencedKeyring(t *testing.T) {
	origFileDelete := pluginsdk.FileDelete
	origReadDir := pluginsdk.ReadDir
	origFileRead := pluginsdk.FileRead
	t.Cleanup(func() {
		pluginsdk.FileDelete = origFileDelete
		pluginsdk.ReadDir = origReadDir
		pluginsdk.FileRead = origFileRead
	})

	calledRemove := false
	pluginsdk.FileDelete = func(string) error {
		calledRemove = true
		return nil
	}
	pluginsdk.ReadDir = func(path string) ([]pluginsdk.DirEntry, error) {
		if path != aptkeyring.DefaultSourcesListDirPath {
			return nil, fmt.Errorf("unexpected dir read path: %q", path)
		}
		return []pluginsdk.DirEntry{{Name: "packages.list"}}, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		switch path {
		case aptkeyring.DefaultSourcesListPath:
			return nil, fmt.Errorf("open %s: no such file or directory", path)
		case filepath.Join(aptkeyring.DefaultSourcesListDirPath, "packages.list"):
			return []byte("deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://example.invalid stable main\n"), nil
		default:
			return nil, fmt.Errorf("unexpected file read path: %q", path)
		}
	}

	if err := removePackageRepoKeyring("/etc/apt/keyrings/kubernetes-apt-keyring.gpg"); err != nil {
		t.Fatalf("removePackageRepoKeyring returned error: %v", err)
	}
	if calledRemove {
		t.Fatal("expected referenced keyring to be preserved")
	}
}

func TestPackageReadReturnsNilWhenManagedRepoKeyringIsMissing(t *testing.T) {
	origGetPackageManager := pluginsdk.GetPackageManager
	origCmdExec := pluginsdk.CmdExec
	origFileStat := pluginsdk.FileStat_
	t.Cleanup(func() {
		pluginsdk.GetPackageManager = origGetPackageManager
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.FileStat_ = origFileStat
	})

	pluginsdk.GetPackageManager = func() (string, error) { return "apt", nil }
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		if cmd != "dpkg-query" {
			t.Fatalf("unexpected command: %s %v", cmd, args)
		}
		return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "ii\t1.2.3\n"}, nil
	}
	pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
		return nil, assertErr("stat /etc/apt/keyrings/kubernetes-apt-keyring.gpg: no such file or directory")
	}

	state, err := (&packageResource{}).Read(pluginsdk.StateData{
		"name":              "gpg",
		"repo_keyring_path": "/etc/apt/keyrings/kubernetes-apt-keyring.gpg",
		"repo_keyring_url":  "https://pkgs.k8s.io/core:/stable:/v1.31/deb/Release.key",
	})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if state != nil {
		t.Fatalf("expected missing repo keyring to force recreation, got %#v", state)
	}
}

func assertErr(message string) error {
	return fmt.Errorf("%s", message)
}

func TestPackageLockReadAptPresentReturnsLockedSubset(t *testing.T) {
	origGetPackageManager := pluginsdk.GetPackageManager
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.GetPackageManager = origGetPackageManager
		pluginsdk.CmdExec = origCmdExec
	})

	pluginsdk.GetPackageManager = func() (string, error) { return "apt", nil }
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		if cmd != "apt-mark" || len(args) != 1 || args[0] != "showhold" {
			t.Fatalf("unexpected command: %s %v", cmd, args)
		}
		return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "kubelet\nkubectl\n"}, nil
	}

	state, err := (&packageLockResource{}).Read(pluginsdk.StateData{"packages": []string{"kubelet", "kubeadm"}})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if got := state.GetStringList("packages"); len(got) != 1 || got[0] != "kubelet" {
		t.Fatalf("unexpected locked package state: %#v", got)
	}
}

func TestPackageLockReadAptAbsentReturnsStableState(t *testing.T) {
	origGetPackageManager := pluginsdk.GetPackageManager
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.GetPackageManager = origGetPackageManager
		pluginsdk.CmdExec = origCmdExec
	})

	pluginsdk.GetPackageManager = func() (string, error) { return "apt", nil }
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		return &pluginsdk.CmdResult{ExitCode: 0}, nil
	}

	state, err := (&packageLockResource{}).Read(pluginsdk.StateData{"packages": []string{"kubelet"}, "ensure": "absent"})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if state.GetString("ensure") != "absent" {
		t.Fatalf("unexpected ensure state: %#v", state)
	}
	if got := state.GetStringList("packages"); len(got) != 1 || got[0] != "kubelet" {
		t.Fatalf("unexpected package state: %#v", got)
	}
}

func TestRPMVersionlockMatches(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		entry string
		name  string
	}{
		{entry: "0:kubelet-1.30.1-1.el9.x86_64", name: "kubelet"},
		{entry: "kubelet-0:1.30.1-1.el9.x86_64", name: "kubelet"},
		{entry: "!kubectl-0:1.30.1-1.el9.x86_64", name: "kubectl"},
	} {
		if !rpmVersionlockMatches(tc.entry, tc.name) {
			t.Fatalf("expected %q to match %q", tc.entry, tc.name)
		}
	}
}

func TestPackageLockCreateUsesDnfVersionlockPerPackage(t *testing.T) {
	origGetPackageManager := pluginsdk.GetPackageManager
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.GetPackageManager = origGetPackageManager
		pluginsdk.CmdExec = origCmdExec
	})

	pluginsdk.GetPackageManager = func() (string, error) { return "dnf", nil }
	commands := make([]string, 0, 3)
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		commands = append(commands, cmd+" "+strings.Join(args, " "))
		if len(args) >= 2 && args[0] == "versionlock" && args[1] == "list" {
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		}
		return &pluginsdk.CmdResult{ExitCode: 0}, nil
	}

	state, err := (&packageLockResource{}).Create(pluginsdk.StateData{"packages": []string{"kubelet", "kubeadm"}})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if state.GetString("id") != "kubeadm,kubelet" {
		t.Fatalf("unexpected resource ID: %#v", state)
	}
	if len(commands) != 3 || commands[0] != "dnf versionlock list" || commands[1] != "dnf -q versionlock add kubelet" || commands[2] != "dnf -q versionlock add kubeadm" {
		t.Fatalf("unexpected commands: %#v", commands)
	}
}

func TestPackageResourceCreateRefreshesCacheAndInstallsPackage(t *testing.T) {
	origGetPackageManager := pluginsdk.GetPackageManager
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.GetPackageManager = origGetPackageManager
		pluginsdk.CmdExec = origCmdExec
	})

	pluginsdk.GetPackageManager = func() (string, error) { return "dnf", nil }
	type call struct {
		cmd  string
		args []string
	}
	var calls []call
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		calls = append(calls, call{cmd: cmd, args: append([]string(nil), args...)})
		switch len(calls) {
		case 1:
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		case 2:
			return &pluginsdk.CmdResult{ExitCode: 1}, nil
		case 3:
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		case 4:
			return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "1.2.3-1"}, nil
		default:
			t.Fatalf("unexpected extra command: %s %#v", cmd, args)
			return nil, nil
		}
	}

	state, err := (&packageResource{}).Create(pluginsdk.StateData{
		"name":         "nginx",
		"version":      "1.2.3-1",
		"update_cache": true,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	wantCalls := []call{
		{cmd: "dnf", args: []string{"makecache", "-y"}},
		{cmd: "rpm", args: []string{"-q", "--queryformat", "%{VERSION}-%{RELEASE}", "nginx"}},
		{cmd: "dnf", args: []string{"install", "-y", "nginx-1.2.3-1"}},
		{cmd: "rpm", args: []string{"-q", "--queryformat", "%{VERSION}-%{RELEASE}", "nginx"}},
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("unexpected commands:\nwant %#v\n got %#v", wantCalls, calls)
	}
	if state.GetString("id") != "nginx" || state.GetString("version") != "1.2.3-1" || !state.GetBool("update_cache") {
		t.Fatalf("unexpected created state: %#v", state)
	}
}

func TestPackageResourceUpdateRemovesOldRepoKeyringAndNullsState(t *testing.T) {
	origGetPackageManager := pluginsdk.GetPackageManager
	origCmdExec := pluginsdk.CmdExec
	origReadDir := pluginsdk.ReadDir
	origFileRead := pluginsdk.FileRead
	origFileDelete := pluginsdk.FileDelete
	t.Cleanup(func() {
		pluginsdk.GetPackageManager = origGetPackageManager
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.ReadDir = origReadDir
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileDelete = origFileDelete
	})

	oldPath := "/etc/apt/keyrings/legacy.gpg"
	pluginsdk.GetPackageManager = func() (string, error) { return "dnf", nil }
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		if cmd != "rpm" {
			t.Fatalf("unexpected command: %s %#v", cmd, args)
		}
		return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "1.2.3-1"}, nil
	}
	pluginsdk.ReadDir = func(path string) ([]pluginsdk.DirEntry, error) {
		if path != aptkeyring.DefaultSourcesListDirPath {
			t.Fatalf("unexpected sources dir path: %q", path)
		}
		return nil, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		if path != aptkeyring.DefaultSourcesListPath {
			t.Fatalf("unexpected file read path: %q", path)
		}
		return nil, assertErr("open /etc/apt/sources.list: no such file or directory")
	}
	var deletedPath string
	pluginsdk.FileDelete = func(path string) error {
		deletedPath = path
		return nil
	}

	state, err := (&packageResource{}).Update(
		pluginsdk.StateData{
			"name":              "nginx",
			"repo_keyring_path": oldPath,
			"repo_keyring_url":  "https://example.invalid/legacy.gpg",
		},
		pluginsdk.StateData{"name": "nginx"},
	)
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if deletedPath != oldPath {
		t.Fatalf("deleted repo keyring = %q, want %q", deletedPath, oldPath)
	}
	if value, ok := state["repo_keyring_path"]; !ok || value != nil {
		t.Fatalf("expected repo_keyring_path to be preserved as null, got %#v", state)
	}
	if value, ok := state["repo_keyring_url"]; !ok || value != nil {
		t.Fatalf("expected repo_keyring_url to be preserved as null, got %#v", state)
	}
	if state.GetString("version") != "1.2.3-1" {
		t.Fatalf("unexpected updated state: %#v", state)
	}
}

func TestPackageResourceDeleteRemovesPackageAndKeyring(t *testing.T) {
	origGetPackageManager := pluginsdk.GetPackageManager
	origCmdExec := pluginsdk.CmdExec
	origReadDir := pluginsdk.ReadDir
	origFileRead := pluginsdk.FileRead
	origFileDelete := pluginsdk.FileDelete
	t.Cleanup(func() {
		pluginsdk.GetPackageManager = origGetPackageManager
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.ReadDir = origReadDir
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileDelete = origFileDelete
	})

	pluginsdk.GetPackageManager = func() (string, error) { return "apt", nil }
	type call struct {
		cmd  string
		args []string
	}
	var calls []call
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		calls = append(calls, call{cmd: cmd, args: append([]string(nil), args...)})
		switch len(calls) {
		case 1:
			return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "ii\t1.2.3\n"}, nil
		case 2:
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		default:
			t.Fatalf("unexpected extra command: %s %#v", cmd, args)
			return nil, nil
		}
	}
	pluginsdk.ReadDir = func(path string) ([]pluginsdk.DirEntry, error) {
		if path != aptkeyring.DefaultSourcesListDirPath {
			t.Fatalf("unexpected sources dir path: %q", path)
		}
		return nil, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		if path != aptkeyring.DefaultSourcesListPath {
			t.Fatalf("unexpected file read path: %q", path)
		}
		return nil, assertErr("open /etc/apt/sources.list: no such file or directory")
	}
	var deletedPath string
	pluginsdk.FileDelete = func(path string) error {
		deletedPath = path
		return nil
	}

	err := (&packageResource{}).Delete(pluginsdk.StateData{
		"name":              "nginx",
		"repo_keyring_path": "/etc/apt/keyrings/nginx.gpg",
	})
	if err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	wantCalls := []call{
		{cmd: "dpkg-query", args: []string{"-W", "-f=${db:Status-Abbrev}\t${Version}", "nginx"}},
		{cmd: "env", args: []string{"DEBIAN_FRONTEND=noninteractive", "apt-get", "purge", "-y", "--allow-change-held-packages", "nginx"}},
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("unexpected commands:\nwant %#v\n got %#v", wantCalls, calls)
	}
	if deletedPath != "/etc/apt/keyrings/nginx.gpg" {
		t.Fatalf("deleted keyring path = %q", deletedPath)
	}
}

func TestPackageResourceImportStateRejectsEmptyAndReadsInstalledPackage(t *testing.T) {
	origGetPackageManager := pluginsdk.GetPackageManager
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.GetPackageManager = origGetPackageManager
		pluginsdk.CmdExec = origCmdExec
	})

	if _, err := (&packageResource{}).ImportState(""); err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("expected empty import ID error, got %v", err)
	}

	pluginsdk.GetPackageManager = func() (string, error) { return "dnf", nil }
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		if cmd != "rpm" {
			t.Fatalf("unexpected command: %s %#v", cmd, args)
		}
		return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "1.2.3-1"}, nil
	}

	state, err := (&packageResource{}).ImportState("nginx")
	if err != nil {
		t.Fatalf("ImportState returned error: %v", err)
	}
	if state.GetString("id") != "nginx" || state.GetString("version") != "1.2.3-1" {
		t.Fatalf("unexpected imported state: %#v", state)
	}
}

func TestPackageLockValidateDeleteAndImport(t *testing.T) {
	origGetPackageManager := pluginsdk.GetPackageManager
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.GetPackageManager = origGetPackageManager
		pluginsdk.CmdExec = origCmdExec
	})

	if err := (&packageLockResource{}).Validate(pluginsdk.StateData{}); err == nil || !strings.Contains(err.Error(), "at least one package name") {
		t.Fatalf("expected package list validation error, got %v", err)
	}

	pluginsdk.GetPackageManager = func() (string, error) { return "apt", nil }
	var calls []string
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		calls = append(calls, cmd+" "+strings.Join(args, " "))
		switch len(calls) {
		case 1:
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		case 2:
			return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "kubelet\n"}, nil
		default:
			t.Fatalf("unexpected extra command: %s %#v", cmd, args)
			return nil, nil
		}
	}

	err := (&packageLockResource{}).Delete(pluginsdk.StateData{"packages": []string{"kubelet", "kubeadm"}})
	if err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	state, err := (&packageLockResource{}).ImportState("kubelet,kubeadm")
	if err != nil {
		t.Fatalf("ImportState returned error: %v", err)
	}
	wantCalls := []string{
		"apt-mark unhold kubelet kubeadm",
		"apt-mark showhold",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("unexpected commands:\nwant %#v\n got %#v", wantCalls, calls)
	}
	if got := state.GetStringList("packages"); len(got) != 1 || got[0] != "kubelet" {
		t.Fatalf("unexpected imported lock state: %#v", state)
	}
}
