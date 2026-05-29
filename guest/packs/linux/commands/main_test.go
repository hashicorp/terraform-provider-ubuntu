// Copyright IBM Corp. 2026

package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
)

func TestCommandValidateRequiresGuard(t *testing.T) {
	t.Parallel()

	resource := &commandResource{}
	err := resource.Validate(pluginsdk.StateData{
		"name":    "bootstrap",
		"command": "echo hi",
	})
	if err == nil || !strings.Contains(err.Error(), "creates or unless") {
		t.Fatalf("expected missing guard validation error, got %v", err)
	}
}

func TestCommandCreateSkipsWhenCreatesExists(t *testing.T) {
	resource := &commandResource{}
	originalFileStat := pluginsdk.FileStat_
	originalCmdExec := pluginsdk.CmdExec
	defer func() {
		pluginsdk.FileStat_ = originalFileStat
		pluginsdk.CmdExec = originalCmdExec
	}()

	pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
		if path != "/etc/kubernetes/admin.conf" {
			t.Fatalf("unexpected creates path: %s", path)
		}
		return &pluginsdk.FileStat{Path: path}, nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		t.Fatalf("command should not have executed, got %s %v", cmd, args)
		return nil, nil
	}

	state, err := resource.Create(pluginsdk.StateData{
		"name":    "kubeadm_init",
		"command": "kubeadm init --config /root/kubeadm.yaml",
		"creates": "/etc/kubernetes/admin.conf",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if got := state.GetString("id"); got != "kubeadm_init" {
		t.Fatalf("unexpected id: %q", got)
	}
	if got := state.GetInt("exit_code"); got != 0 {
		t.Fatalf("unexpected exit code: %d", got)
	}
}

func TestCommandCreateExecutesWithDefaults(t *testing.T) {
	resource := &commandResource{}
	originalFileStat := pluginsdk.FileStat_
	originalCmdExec := pluginsdk.CmdExec
	originalLogInfo := pluginsdk.LogInfo
	defer func() {
		pluginsdk.FileStat_ = originalFileStat
		pluginsdk.CmdExec = originalCmdExec
		pluginsdk.LogInfo = originalLogInfo
	}()

	fileStatCalls := 0
	pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
		fileStatCalls++
		if fileStatCalls == 1 {
			return nil, os.ErrNotExist
		}
		return &pluginsdk.FileStat{Path: path}, nil
	}
	pluginsdk.LogInfo = func(string) {}

	var gotCmd string
	var gotArgs []string
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		gotCmd = cmd
		gotArgs = append([]string(nil), args...)
		return &pluginsdk.CmdResult{Stdout: "done\n", ExitCode: 0}, nil
	}

	state, err := resource.Create(pluginsdk.StateData{
		"name":              "install_calico",
		"command":           "kubectl apply -f calico.yaml",
		"creates":           "/var/lib/tf-linux-provider/calico.installed",
		"working_directory": "/root",
		"environment": map[string]string{
			"KUBECONFIG": "/etc/kubernetes/admin.conf",
		},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if gotCmd != "sh" {
		t.Fatalf("expected default interpreter command sh, got %q", gotCmd)
	}
	if len(gotArgs) != 2 || gotArgs[0] != "-lc" {
		t.Fatalf("unexpected interpreter args: %#v", gotArgs)
	}
	if !strings.Contains(gotArgs[1], "cd -- '/root'") {
		t.Fatalf("expected working directory wrapper, got %q", gotArgs[1])
	}
	if !strings.Contains(gotArgs[1], "export KUBECONFIG='/etc/kubernetes/admin.conf'") {
		t.Fatalf("expected environment export, got %q", gotArgs[1])
	}
	if got := state.GetString("stdout"); got != "done\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}
}

func TestCommandCreateRunAsUsesCreatesStat(t *testing.T) {
	resource := &commandResource{}
	originalFileStat := pluginsdk.FileStat_
	originalCmdExec := pluginsdk.CmdExec
	originalLogInfo := pluginsdk.LogInfo
	defer func() {
		pluginsdk.FileStat_ = originalFileStat
		pluginsdk.CmdExec = originalCmdExec
		pluginsdk.LogInfo = originalLogInfo
	}()

	pluginsdk.LogInfo = func(string) {}
	fileStatCalls := 0
	pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
		fileStatCalls++
		if path != "/home/tf/tf-linux-provider-command-tf.txt" {
			t.Fatalf("unexpected creates path: %s", path)
		}
		if fileStatCalls == 1 {
			return nil, os.ErrNotExist
		}
		return &pluginsdk.FileStat{Path: path, Owner: "tf"}, nil
	}

	cmdCalls := 0
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		cmdCalls++
		return &pluginsdk.CmdResult{Stdout: "done\n", ExitCode: 0}, nil
	}

	state, err := resource.Create(pluginsdk.StateData{
		"name":    "tf-artifact",
		"command": "printf 'tf command artifact\n' > /home/tf/tf-linux-provider-command-tf.txt",
		"creates": "/home/tf/tf-linux-provider-command-tf.txt",
		"run_as":  "tf",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if cmdCalls != 1 {
		t.Fatalf("expected command to execute once, got %d", cmdCalls)
	}
	if fileStatCalls != 2 {
		t.Fatalf("expected creates guard to stat twice, got %d", fileStatCalls)
	}
	if got := state.GetString("run_as"); got != "tf" {
		t.Fatalf("unexpected run_as in state: %q", got)
	}
	if got := state.GetString("stdout"); got != "done\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}
}

func TestCommandUpdateAlwaysExecutesPlanCommand(t *testing.T) {
	resource := &commandResource{}
	originalFileStat := pluginsdk.FileStat_
	originalCmdExec := pluginsdk.CmdExec
	originalLogInfo := pluginsdk.LogInfo
	defer func() {
		pluginsdk.FileStat_ = originalFileStat
		pluginsdk.CmdExec = originalCmdExec
		pluginsdk.LogInfo = originalLogInfo
	}()

	pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
		if path != "/var/lib/tf-linux-provider/join-command.generated" {
			t.Fatalf("unexpected creates path: %s", path)
		}
		return &pluginsdk.FileStat{Path: path}, nil
	}
	pluginsdk.LogInfo = func(string) {}

	callCount := 0
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		callCount++
		return &pluginsdk.CmdResult{Stdout: "rotated\n", ExitCode: 0}, nil
	}

	_, err := resource.Update(
		pluginsdk.StateData{
			"name":    "join_command",
			"command": "kubeadm token create --print-join-command",
			"creates": "/var/lib/tf-linux-provider/join-command.generated",
		},
		pluginsdk.StateData{
			"name":    "join_command",
			"command": "kubeadm token create --ttl 0 --print-join-command",
			"creates": "/var/lib/tf-linux-provider/join-command.generated",
		},
	)
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected update to execute command exactly once, got %d", callCount)
	}
}

func TestCommandUpdateClearsRemovedCreatesWhenSwitchingToUnless(t *testing.T) {
	resource := &commandResource{}
	originalCmdExec := pluginsdk.CmdExec
	originalLogInfo := pluginsdk.LogInfo
	defer func() {
		pluginsdk.CmdExec = originalCmdExec
		pluginsdk.LogInfo = originalLogInfo
	}()

	pluginsdk.LogInfo = func(string) {}
	callCount := 0
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		callCount++
		return &pluginsdk.CmdResult{ExitCode: 0}, nil
	}
	unless := "kubectl get daemonset aws-cloud-controller-manager -n kube-system >/dev/null 2>&1\n"

	state, err := resource.Update(
		pluginsdk.StateData{
			"name":           "install_aws_ccm_base",
			"command":        "kubectl apply -f base.yaml",
			"creates":        "/var/lib/tf-linux/aws-ccm-base-v1.31.0.installed",
			"delete_command": "rm -f /var/lib/tf-linux/aws-ccm-base-v1.31.0.installed",
		},
		pluginsdk.StateData{
			"name":    "install_aws_ccm_base",
			"command": "kubectl apply -f base.yaml",
			"unless":  unless,
		},
	)
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected command and unless guard execution, got %d calls", callCount)
	}
	value, ok := state["creates"]
	if !ok {
		t.Fatal("expected creates key to be present so prior state can be cleared")
	}
	if value != nil {
		t.Fatalf("expected creates to be cleared to nil, got %#v", value)
	}
	deleteCommand, ok := state["delete_command"]
	if !ok {
		t.Fatal("expected delete_command key to be present so prior state can be cleared")
	}
	if deleteCommand != nil {
		t.Fatalf("expected delete_command to be cleared to nil, got %#v", deleteCommand)
	}
	if got := state.GetString("unless"); got != unless {
		t.Fatalf("expected unless guard to be preserved exactly, got %#v want %#v", got, unless)
	}
}

func TestCommandReadMissingGuardReturnsNil(t *testing.T) {
	resource := &commandResource{}
	originalFileStat := pluginsdk.FileStat_
	originalCmdExec := pluginsdk.CmdExec
	originalLogInfo := pluginsdk.LogInfo
	defer func() {
		pluginsdk.FileStat_ = originalFileStat
		pluginsdk.CmdExec = originalCmdExec
		pluginsdk.LogInfo = originalLogInfo
	}()

	pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
		return nil, os.ErrNotExist
	}
	pluginsdk.LogInfo = func(string) {}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		return &pluginsdk.CmdResult{ExitCode: 1}, nil
	}

	state, err := resource.Read(pluginsdk.StateData{
		"name":      "worker_join",
		"command":   "kubeadm join ...",
		"creates":   "/etc/kubernetes/kubelet.conf",
		"unless":    "test -f /etc/kubernetes/admin.conf",
		"stdout":    "join command",
		"stderr":    "",
		"exit_code": 0,
	})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if state != nil {
		t.Fatalf("expected nil state when guards are unsatisfied, got %#v", state)
	}
}

func TestCommandReadPreservesPriorResultWhenGuardSatisfied(t *testing.T) {
	resource := &commandResource{}
	originalFileStat := pluginsdk.FileStat_
	originalCmdExec := pluginsdk.CmdExec
	originalLogInfo := pluginsdk.LogInfo
	defer func() {
		pluginsdk.FileStat_ = originalFileStat
		pluginsdk.CmdExec = originalCmdExec
		pluginsdk.LogInfo = originalLogInfo
	}()

	pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
		if path != "/var/lib/tf-linux-provider/join-material.json" {
			t.Fatalf("unexpected creates path: %s", path)
		}
		return &pluginsdk.FileStat{Path: path}, nil
	}
	pluginsdk.LogInfo = func(string) {}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		t.Fatalf("unless guard should not execute when creates exists, got %s %v", cmd, args)
		return nil, nil
	}

	state, err := resource.Read(pluginsdk.StateData{
		"name":      "worker_join_material",
		"command":   "kubeadm token create",
		"creates":   "/var/lib/tf-linux-provider/join-material.json",
		"stdout":    `{"token":"abc123.0123456789abcdef","ca_cert_hash":"sha256:deadbeef"}`,
		"stderr":    "warning\n",
		"exit_code": 0,
	})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if got := state.GetString("stdout"); got != `{"token":"abc123.0123456789abcdef","ca_cert_hash":"sha256:deadbeef"}` {
		t.Fatalf("unexpected stdout: %q", got)
	}
	if got := state.GetString("stderr"); got != "warning\n" {
		t.Fatalf("unexpected stderr: %q", got)
	}
	if got := state.GetInt("exit_code"); got != 0 {
		t.Fatalf("unexpected exit code: %d", got)
	}
}

func TestCommandGuardSatisfiedReportsUnexpectedCreatesErrors(t *testing.T) {
	originalFileStat := pluginsdk.FileStat_
	defer func() {
		pluginsdk.FileStat_ = originalFileStat
	}()

	pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
		return nil, fmt.Errorf("stat %s: permission denied", path)
	}

	_, err := guardSatisfied(pluginsdk.StateData{"creates": "/home/tf/private.txt"})
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("expected creates guard error, got %v", err)
	}
}

func TestCommandDeleteRunsDeleteCommand(t *testing.T) {
	resource := &commandResource{}
	originalCmdExec := pluginsdk.CmdExec
	originalLogInfo := pluginsdk.LogInfo
	defer func() {
		pluginsdk.CmdExec = originalCmdExec
		pluginsdk.LogInfo = originalLogInfo
	}()

	pluginsdk.LogInfo = func(string) {}
	called := false
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		called = true
		return &pluginsdk.CmdResult{ExitCode: 0}, nil
	}

	err := resource.Delete(pluginsdk.StateData{
		"name":           "reset_worker",
		"command":        "kubeadm join ...",
		"creates":        "/etc/kubernetes/kubelet.conf",
		"delete_command": "kubeadm reset -f",
	})
	if err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if !called {
		t.Fatal("expected delete command to execute")
	}
}

func TestRunCommandActionExecutesWithDefaults(t *testing.T) {
	action := &runCommandAction{}
	originalCmdExec := pluginsdk.CmdExec
	originalLogInfo := pluginsdk.LogInfo
	defer func() {
		pluginsdk.CmdExec = originalCmdExec
		pluginsdk.LogInfo = originalLogInfo
	}()

	pluginsdk.LogInfo = func(string) {}

	var gotCmd string
	var gotArgs []string
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		gotCmd = cmd
		gotArgs = append([]string(nil), args...)
		return &pluginsdk.CmdResult{Stdout: "reloaded\n", ExitCode: 0}, nil
	}

	state, err := action.Invoke(pluginsdk.StateData{
		"name":              "reload_nginx",
		"command":           "nginx -s reload",
		"working_directory": "/root",
		"environment": map[string]string{
			"KUBECONFIG": "/etc/kubernetes/admin.conf",
		},
	})
	if err != nil {
		t.Fatalf("Invoke returned error: %v", err)
	}
	if gotCmd != "sh" {
		t.Fatalf("expected default interpreter command sh, got %q", gotCmd)
	}
	if len(gotArgs) != 2 || gotArgs[0] != "-lc" {
		t.Fatalf("unexpected interpreter args: %#v", gotArgs)
	}
	if !strings.Contains(gotArgs[1], "cd -- '/root'") {
		t.Fatalf("expected working directory wrapper, got %q", gotArgs[1])
	}
	if !strings.Contains(gotArgs[1], "export KUBECONFIG='/etc/kubernetes/admin.conf'") {
		t.Fatalf("expected environment export, got %q", gotArgs[1])
	}
	if got := state.GetString("stdout"); got != "reloaded\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}
	if got := state.GetInt("exit_code"); got != 0 {
		t.Fatalf("unexpected exit code: %d", got)
	}
}

func TestRunCommandActionDoesNotRequireGuard(t *testing.T) {
	action := &runCommandAction{}
	originalCmdExec := pluginsdk.CmdExec
	originalLogInfo := pluginsdk.LogInfo
	defer func() {
		pluginsdk.CmdExec = originalCmdExec
		pluginsdk.LogInfo = originalLogInfo
	}()

	pluginsdk.LogInfo = func(string) {}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		return &pluginsdk.CmdResult{ExitCode: 0}, nil
	}

	if _, err := action.Invoke(pluginsdk.StateData{
		"name":    "reload_nginx",
		"command": "nginx -s reload",
	}); err != nil {
		t.Fatalf("Invoke returned error: %v", err)
	}
}
