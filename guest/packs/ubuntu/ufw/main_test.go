// Copyright IBM Corp. 2026

package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
)

func TestDesiredUFWRuleSpecDefaults(t *testing.T) {
	t.Parallel()

	spec, err := desiredUFWRuleSpec(pluginsdk.StateData{
		"name": "ssh",
		"port": "22",
	})
	if err != nil {
		t.Fatalf("desiredUFWRuleSpec returned error: %v", err)
	}
	if spec.Action != "allow" || spec.Direction != "in" || spec.Protocol != "tcp" {
		t.Fatalf("unexpected defaults: %#v", spec)
	}
	if spec.RuleComment != "tf-linux-provider:name=ssh" {
		t.Fatalf("unexpected managed comment %q", spec.RuleComment)
	}
}

func TestParseUFWStatusNumbered(t *testing.T) {
	t.Parallel()

	rules := parseUFWStatusNumbered(`Status: active

[ 1] 22/tcp                     ALLOW IN    Anywhere                   # tf-linux-provider:name=ssh
[ 2] 6443/tcp                   ALLOW IN    10.0.0.0/8                 # some other rule
[ 3] 53/udp                     DENY OUT    Anywhere                   # tf-linux-provider:name=dns-egress
`)
	if len(rules) != 2 {
		t.Fatalf("expected 2 managed rules, got %#v", rules)
	}
	if rules[0].Number != 1 || rules[1].Comment != "tf-linux-provider:name=dns-egress" {
		t.Fatalf("unexpected parsed rules: %#v", rules)
	}
}

func TestApplyUFWRuleRunsDeleteThenAdd(t *testing.T) {
	origCmdExists := pluginsdk.CmdExists
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.CmdExists = origCmdExists
		pluginsdk.CmdExec = origCmdExec
	})

	pluginsdk.CmdExists = func(name string) (bool, error) {
		return name == "ufw", nil
	}
	commands := make([]string, 0, 4)
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		commands = append(commands, cmd+" "+strings.Join(args, " "))
		if len(args) >= 2 && args[0] == "status" && args[1] == "verbose" {
			return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "Status: active\nDefault: deny (incoming), allow (outgoing), disabled (routed)\n"}, nil
		}
		if len(args) >= 2 && args[0] == "status" && args[1] == "numbered" {
			return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "[ 3] 22/tcp ALLOW IN Anywhere # tf-linux-provider:name=ssh\n"}, nil
		}
		return &pluginsdk.CmdResult{ExitCode: 0}, nil
	}

	_, err := applyUFWRule(nil, pluginsdk.StateData{
		"name": "ssh",
		"port": "22",
	})
	if err != nil {
		t.Fatalf("applyUFWRule returned error: %v", err)
	}

	want := []string{
		"ufw status verbose",
		"ufw status numbered",
		"ufw status numbered",
		"ufw --force delete 3",
		"ufw allow in from any to any port 22 proto tcp comment tf-linux-provider:name=ssh",
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("unexpected commands:\nwant %#v\n got %#v", want, commands)
	}
}

func TestApplyUFWRuleBlocksSSHDisconnectWithoutOverride(t *testing.T) {
	origCmdExists := pluginsdk.CmdExists
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.CmdExists = origCmdExists
		pluginsdk.CmdExec = origCmdExec
	})

	pluginsdk.CmdExists = func(name string) (bool, error) {
		return name == "ufw", nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		switch {
		case len(args) >= 2 && args[0] == "status" && args[1] == "verbose":
			return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "Status: active\nDefault: deny (incoming), allow (outgoing), disabled (routed)\n"}, nil
		case len(args) >= 2 && args[0] == "status" && args[1] == "numbered":
			return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "[ 1] 22/tcp ALLOW IN Anywhere # tf-linux-provider:name=ssh\n"}, nil
		default:
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		}
	}

	_, err := applyUFWRule(
		pluginsdk.StateData{"name": "ssh", "port": "22"},
		pluginsdk.StateData{"name": "ssh", "port": "6443"},
	)
	if err == nil {
		t.Fatal("expected SSH safety error")
	}
	if !strings.Contains(err.Error(), "allow_ssh_disconnect=true") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyUFWRuleAllowsOverride(t *testing.T) {
	origCmdExists := pluginsdk.CmdExists
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.CmdExists = origCmdExists
		pluginsdk.CmdExec = origCmdExec
	})

	pluginsdk.CmdExists = func(name string) (bool, error) {
		return name == "ufw", nil
	}
	commands := make([]string, 0, 8)
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		commands = append(commands, cmd+" "+strings.Join(args, " "))
		switch {
		case len(args) >= 2 && args[0] == "status" && args[1] == "verbose":
			return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "Status: active\nDefault: deny (incoming), allow (outgoing), disabled (routed)\n"}, nil
		case len(args) >= 2 && args[0] == "status" && args[1] == "numbered":
			return &pluginsdk.CmdResult{ExitCode: 0, Stdout: "[ 1] 22/tcp ALLOW IN Anywhere # tf-linux-provider:name=ssh\n"}, nil
		default:
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		}
	}

	_, err := applyUFWRule(
		pluginsdk.StateData{"name": "ssh", "port": "22"},
		pluginsdk.StateData{"name": "ssh", "port": "6443", "allow_ssh_disconnect": true},
	)
	if err != nil {
		t.Fatalf("applyUFWRule returned error: %v", err)
	}
	if len(commands) == 0 || !strings.Contains(strings.Join(commands, "\n"), "ufw --force delete 1") {
		t.Fatalf("expected override path to delete and replace the managed SSH rule, got %#v", commands)
	}
}
