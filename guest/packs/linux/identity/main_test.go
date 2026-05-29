// Copyright IBM Corp. 2026

package main

import (
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
)

func TestBuildUseraddArgsDefaultsCreateHome(t *testing.T) {
	t.Parallel()

	args := buildUseraddArgs(pluginsdk.StateData{
		"name":          "alice",
		"primary_group": "developers",
		"home":          "/home/alice",
	})

	want := []string{"-g", "developers", "-d", "/home/alice", "-m"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("unexpected useradd args: got %#v want %#v", args, want)
	}
}

func TestBuildUsermodArgsAppendGroups(t *testing.T) {
	t.Parallel()

	args := buildUsermodArgs(pluginsdk.StateData{
		"groups":        []string{"wheel", "docker"},
		"append_groups": true,
		"move_home":     true,
		"home":          "/srv/alice",
	})

	want := []string{"-d", "/srv/alice", "-m", "-a", "-G", "wheel,docker"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("unexpected usermod args: got %#v want %#v", args, want)
	}
}

func TestBoolWithDefault(t *testing.T) {
	t.Parallel()

	if !boolWithDefault(pluginsdk.StateData{}, "create_home", true) {
		t.Fatal("expected missing key to use default")
	}
	if boolWithDefault(pluginsdk.StateData{"create_home": false}, "create_home", true) {
		t.Fatal("expected explicit false to override default")
	}
}

func TestEnsureDefaultsToPresent(t *testing.T) {
	t.Parallel()

	if got := userEnsure(nil); got != "present" {
		t.Fatalf("unexpected default user ensure: got %q want present", got)
	}
	if got := groupEnsure(nil); got != "present" {
		t.Fatalf("unexpected default group ensure: got %q want present", got)
	}
	if got := userEnsure(pluginsdk.StateData{"ensure": "absent"}); got != "absent" {
		t.Fatalf("unexpected explicit user ensure: got %q want absent", got)
	}
	if got := groupEnsure(pluginsdk.StateData{"ensure": "absent"}); got != "absent" {
		t.Fatalf("unexpected explicit group ensure: got %q want absent", got)
	}
}

func TestAbsentStateHelpers(t *testing.T) {
	t.Parallel()

	groupState := absentGroupState(pluginsdk.StateData{"name": "ops"})
	if groupState.GetString("ensure") != "absent" {
		t.Fatalf("unexpected group ensure: %q", groupState.GetString("ensure"))
	}

	userState := absentUserState(pluginsdk.StateData{"name": "alice", "remove_home": true})
	if userState.GetString("ensure") != "absent" {
		t.Fatalf("unexpected user ensure: %q", userState.GetString("ensure"))
	}
	if !userState.GetBool("remove_home") {
		t.Fatal("expected remove_home to be preserved for absent user state")
	}
}

func TestReadGroupUsesLookupGroupCapability(t *testing.T) {
	origLookupGroup := pluginsdk.LookupGroup
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.LookupGroup = origLookupGroup
		pluginsdk.CmdExec = origCmdExec
	})

	pluginsdk.LookupGroup = func(name string) (*pluginsdk.IdentityGroup, error) {
		if name != "developers" {
			t.Fatalf("unexpected group lookup %q", name)
		}
		return &pluginsdk.IdentityGroup{Name: "developers", GID: 1001}, nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		t.Fatalf("unexpected command execution: %s %#v", cmd, args)
		return nil, nil
	}

	state, err := readGroup("developers")
	if err != nil {
		t.Fatalf("readGroup returned error: %v", err)
	}
	if state.GetString("name") != "developers" || state.GetInt("gid") != 1001 {
		t.Fatalf("unexpected group state: %#v", state)
	}
}

func TestReadUserUsesLookupUserCapability(t *testing.T) {
	origLookupUser := pluginsdk.LookupUser
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.LookupUser = origLookupUser
		pluginsdk.CmdExec = origCmdExec
	})

	pluginsdk.LookupUser = func(name string) (*pluginsdk.IdentityUser, error) {
		if name != "alice" {
			t.Fatalf("unexpected user lookup %q", name)
		}
		return &pluginsdk.IdentityUser{
			Name:         "alice",
			UID:          1000,
			GID:          1000,
			Comment:      "Alice Example",
			Home:         "/home/alice",
			Shell:        "/bin/bash",
			PrimaryGroup: "developers",
			Groups:       []string{"docker", "wheel"},
		}, nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		t.Fatalf("unexpected command execution: %s %#v", cmd, args)
		return nil, nil
	}

	state, err := readUser("alice")
	if err != nil {
		t.Fatalf("readUser returned error: %v", err)
	}
	if state.GetString("name") != "alice" || state.GetInt("uid") != 1000 || state.GetString("primary_group") != "developers" {
		t.Fatalf("unexpected user state: %#v", state)
	}
	if got := state.GetStringList("groups"); !reflect.DeepEqual(got, []string{"docker", "wheel"}) {
		t.Fatalf("unexpected groups: %#v", got)
	}
}
