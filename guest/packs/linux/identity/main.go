package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
	linuxidentitycontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/linuxidentity"
)

type groupResource struct{}

func (r *groupResource) Name() string { return "group" }

func (r *groupResource) Schema() pluginsdk.Schema {
	return linuxidentitycontract.GroupResourceSchema()
}

func (r *groupResource) Validate(config pluginsdk.StateData) error {
	if strings.TrimSpace(config.GetString("name")) == "" {
		return fmt.Errorf("name must not be empty")
	}
	ensure := groupEnsure(config)
	if ensure != "present" && ensure != "absent" {
		return fmt.Errorf("ensure must be \"present\" or \"absent\", got %q", ensure)
	}
	if ensure == "absent" {
		if _, ok := config["gid"]; ok {
			return fmt.Errorf("gid cannot be set when ensure is \"absent\"")
		}
		if config.GetBool("system") {
			return fmt.Errorf("system cannot be set when ensure is \"absent\"")
		}
	}
	return nil
}

func (r *groupResource) Read(state pluginsdk.StateData) (pluginsdk.StateData, error) {
	name := state.GetString("name")
	current, err := readGroup(name)
	if err != nil {
		return nil, err
	}
	if current == nil {
		if groupEnsure(state) == "absent" {
			return absentGroupState(state), nil
		}
		return nil, nil
	}
	current["ensure"] = "present"
	return current, nil
}

func (r *groupResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	name := plan.GetString("name")
	if groupEnsure(plan) == "absent" {
		state, err := readGroup(name)
		if err != nil {
			return nil, err
		}
		if state != nil {
			if err := r.Delete(state); err != nil {
				return nil, err
			}
		}
		return absentGroupState(plan), nil
	}
	args := buildGroupaddArgs(plan)
	args = append(args, name)

	result, err := pluginsdk.CmdExec("groupadd", args)
	if err != nil {
		return nil, fmt.Errorf("groupadd: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("groupadd failed (exit %d): %s", result.ExitCode, result.Stderr)
	}

	state, err := readGroup(name)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, fmt.Errorf("group %q not found after creation", name)
	}
	if plan.GetBool("system") {
		state["system"] = true
	}
	state["ensure"] = "present"
	state["id"] = name
	return state, nil
}

func (r *groupResource) Update(prior, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	name := prior.GetString("name")
	if groupEnsure(plan) == "absent" {
		if err := r.Delete(prior); err != nil {
			return nil, err
		}
		return absentGroupState(plan), nil
	}
	args := buildGroupmodArgs(plan)
	if len(args) > 0 {
		args = append(args, name)
		result, err := pluginsdk.CmdExec("groupmod", args)
		if err != nil {
			return nil, fmt.Errorf("groupmod: %w", err)
		}
		if result.ExitCode != 0 {
			return nil, fmt.Errorf("groupmod failed (exit %d): %s", result.ExitCode, result.Stderr)
		}
	}

	state, err := readGroup(name)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, fmt.Errorf("group %q not found after update", name)
	}
	if plan.GetBool("system") {
		state["system"] = true
	}
	state["ensure"] = "present"
	state["id"] = name
	return state, nil
}

func (r *groupResource) Delete(state pluginsdk.StateData) error {
	name := state.GetString("name")
	result, err := pluginsdk.CmdExec("groupdel", []string{name})
	if err != nil {
		return fmt.Errorf("groupdel: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("groupdel failed (exit %d): %s", result.ExitCode, result.Stderr)
	}
	return nil
}

func (r *groupResource) ImportState(id string) (pluginsdk.StateData, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("import ID (group name) must not be empty")
	}
	state, err := readGroup(id)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, fmt.Errorf("group %q not found", id)
	}
	state["ensure"] = "present"
	return state, nil
}

type userResource struct{}

func (r *userResource) Name() string { return "user" }

func (r *userResource) Schema() pluginsdk.Schema {
	return linuxidentitycontract.UserResourceSchema()
}

func (r *userResource) Validate(config pluginsdk.StateData) error {
	if strings.TrimSpace(config.GetString("name")) == "" {
		return fmt.Errorf("name must not be empty")
	}
	ensure := userEnsure(config)
	if ensure != "present" && ensure != "absent" {
		return fmt.Errorf("ensure must be \"present\" or \"absent\", got %q", ensure)
	}
	if home := strings.TrimSpace(config.GetString("home")); home != "" && !strings.HasPrefix(home, "/") {
		return fmt.Errorf("home must be an absolute path, got %q", home)
	}
	if shell := strings.TrimSpace(config.GetString("shell")); shell != "" && !strings.HasPrefix(shell, "/") {
		return fmt.Errorf("shell must be an absolute path, got %q", shell)
	}
	if config.GetBool("move_home") && strings.TrimSpace(config.GetString("home")) == "" {
		return fmt.Errorf("move_home requires home to be set")
	}
	if config.GetBool("append_groups") {
		if _, ok := config["groups"]; !ok {
			return fmt.Errorf("append_groups requires groups to be set")
		}
	}
	for _, group := range config.GetStringList("groups") {
		if strings.TrimSpace(group) == "" {
			return fmt.Errorf("groups must not contain empty values")
		}
	}
	if _, ok := config["primary_group"]; ok && strings.TrimSpace(config.GetString("primary_group")) == "" {
		return fmt.Errorf("primary_group must not be empty when set")
	}
	if ensure == "absent" {
		for _, key := range []string{"uid", "gid", "primary_group", "home", "shell", "groups", "append_groups", "create_home", "move_home", "comment", "run_as"} {
			if _, ok := config[key]; ok {
				return fmt.Errorf("%s cannot be set when ensure is \"absent\"", key)
			}
		}
		if config.GetBool("system") {
			return fmt.Errorf("system cannot be set when ensure is \"absent\"")
		}
	}
	return nil
}

func (r *userResource) Read(state pluginsdk.StateData) (pluginsdk.StateData, error) {
	name := state.GetString("name")
	current, err := readUser(name)
	if err != nil {
		return nil, err
	}
	if current == nil {
		if userEnsure(state) == "absent" {
			return absentUserState(state), nil
		}
		return nil, nil
	}
	current["ensure"] = "present"
	return current, nil
}

func (r *userResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	name := plan.GetString("name")
	if userEnsure(plan) == "absent" {
		state, err := readUser(name)
		if err != nil {
			return nil, err
		}
		if state != nil {
			if err := r.Delete(carryUserPlanFields(state, plan)); err != nil {
				return nil, err
			}
		}
		return absentUserState(plan), nil
	}
	if err := ensureUserGroupsExist(plan); err != nil {
		return nil, err
	}
	args := buildUseraddArgs(plan)
	args = append(args, name)

	result, err := pluginsdk.CmdExec("useradd", args)
	if err != nil {
		return nil, fmt.Errorf("useradd: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("useradd failed (exit %d): %s", result.ExitCode, result.Stderr)
	}

	state, err := readUser(name)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, fmt.Errorf("user %q not found after creation", name)
	}
	state = carryUserPlanFields(state, plan)
	state["ensure"] = "present"
	return state, nil
}

func (r *userResource) Update(prior, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	name := prior.GetString("name")
	if userEnsure(plan) == "absent" {
		if err := r.Delete(carryUserPlanFields(prior, plan)); err != nil {
			return nil, err
		}
		return absentUserState(plan), nil
	}
	if err := ensureUserGroupsExist(plan); err != nil {
		return nil, err
	}
	args := buildUsermodArgs(plan)
	if len(args) > 0 {
		args = append(args, name)
		result, err := pluginsdk.CmdExec("usermod", args)
		if err != nil {
			return nil, fmt.Errorf("usermod: %w", err)
		}
		if result.ExitCode != 0 {
			return nil, fmt.Errorf("usermod failed (exit %d): %s", result.ExitCode, result.Stderr)
		}
	}

	state, err := readUser(name)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, fmt.Errorf("user %q not found after update", name)
	}
	state = carryUserPlanFields(state, plan)
	state["ensure"] = "present"
	return state, nil
}

func (r *userResource) Delete(state pluginsdk.StateData) error {
	name := state.GetString("name")
	args := []string{name}
	if boolWithDefault(state, "remove_home", false) {
		args = append([]string{"-r"}, args...)
	}
	result, err := pluginsdk.CmdExec("userdel", args)
	if err != nil {
		return fmt.Errorf("userdel: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("userdel failed (exit %d): %s", result.ExitCode, result.Stderr)
	}
	return nil
}

func (r *userResource) ImportState(id string) (pluginsdk.StateData, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("import ID (username) must not be empty")
	}
	state, err := readUser(id)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, fmt.Errorf("user %q not found", id)
	}
	state["ensure"] = "present"
	return state, nil
}

func readGroup(name string) (pluginsdk.StateData, error) {
	identity, err := pluginsdk.LookupGroup(name)
	if err != nil {
		return nil, fmt.Errorf("lookup group %s: %w", name, err)
	}
	if identity == nil {
		return nil, nil
	}
	return pluginsdk.StateData{"id": identity.Name, "name": identity.Name, "gid": identity.GID, "ensure": "present"}, nil
}

func buildGroupaddArgs(plan pluginsdk.StateData) []string {
	var args []string
	if plan.GetBool("system") {
		args = append(args, "-r")
	}
	if gid := plan.GetInt("gid"); gid != 0 {
		args = append(args, "-g", strconv.Itoa(gid))
	}
	return args
}

func buildGroupmodArgs(plan pluginsdk.StateData) []string {
	var args []string
	if gid := plan.GetInt("gid"); gid != 0 {
		args = append(args, "-g", strconv.Itoa(gid))
	}
	return args
}

func readUser(name string) (pluginsdk.StateData, error) {
	identity, err := pluginsdk.LookupUser(name)
	if err != nil {
		return nil, fmt.Errorf("lookup user %s: %w", name, err)
	}
	if identity == nil {
		return nil, nil
	}
	state := pluginsdk.StateData{
		"id":      identity.Name,
		"name":    identity.Name,
		"ensure":  "present",
		"uid":     identity.UID,
		"gid":     identity.GID,
		"comment": identity.Comment,
		"home":    identity.Home,
		"shell":   identity.Shell,
	}
	if identity.PrimaryGroup != "" {
		state["primary_group"] = identity.PrimaryGroup
	}
	if len(identity.Groups) > 0 {
		state["groups"] = append([]string(nil), identity.Groups...)
	}
	return state, nil
}

func buildUseraddArgs(plan pluginsdk.StateData) []string {
	var args []string
	if plan.GetBool("system") {
		args = append(args, "-r")
	}
	if uid := plan.GetInt("uid"); uid != 0 {
		args = append(args, "-u", strconv.Itoa(uid))
	}
	if primaryGroup := plan.GetString("primary_group"); primaryGroup != "" {
		args = append(args, "-g", primaryGroup)
	}
	createHome := boolWithDefault(plan, "create_home", true)
	if home := plan.GetString("home"); home != "" {
		args = append(args, "-d", home)
	}
	if createHome {
		args = append(args, "-m")
	} else {
		args = append(args, "-M")
	}
	if shell := plan.GetString("shell"); shell != "" {
		args = append(args, "-s", shell)
	}
	if comment := plan.GetString("comment"); comment != "" {
		args = append(args, "-c", comment)
	}
	if groups := plan.GetStringList("groups"); len(groups) > 0 {
		args = append(args, "-G", strings.Join(groups, ","))
	}
	return args
}

func buildUsermodArgs(plan pluginsdk.StateData) []string {
	var args []string
	if primaryGroup := plan.GetString("primary_group"); primaryGroup != "" {
		args = append(args, "-g", primaryGroup)
	}
	if shell := plan.GetString("shell"); shell != "" {
		args = append(args, "-s", shell)
	}
	if home := plan.GetString("home"); home != "" {
		args = append(args, "-d", home)
		if plan.GetBool("move_home") {
			args = append(args, "-m")
		}
	}
	if comment := plan.GetString("comment"); comment != "" {
		args = append(args, "-c", comment)
	}
	if _, ok := plan["groups"]; ok {
		groups := plan.GetStringList("groups")
		if plan.GetBool("append_groups") && len(groups) > 0 {
			args = append(args, "-a", "-G", strings.Join(groups, ","))
		} else {
			args = append(args, "-G", strings.Join(groups, ","))
		}
	}
	return args
}

func carryUserPlanFields(state, plan pluginsdk.StateData) pluginsdk.StateData {
	if plan.GetBool("system") {
		state["system"] = true
	}
	if _, ok := plan["groups"]; ok {
		groups := plan.GetStringList("groups")
		state["groups"] = groups
	}
	if v := plan.GetString("primary_group"); v != "" {
		state["primary_group"] = v
	}
	if _, ok := plan["append_groups"]; ok {
		state["append_groups"] = plan.GetBool("append_groups")
	}
	if _, ok := plan["create_home"]; ok {
		state["create_home"] = plan.GetBool("create_home")
	}
	if _, ok := plan["move_home"]; ok {
		state["move_home"] = plan.GetBool("move_home")
	}
	if _, ok := plan["remove_home"]; ok {
		state["remove_home"] = plan.GetBool("remove_home")
	}
	if runAs := plan.GetString("run_as"); runAs != "" {
		state["run_as"] = runAs
	}
	return state
}

func ensureUserGroupsExist(plan pluginsdk.StateData) error {
	if primaryGroup := strings.TrimSpace(plan.GetString("primary_group")); primaryGroup != "" {
		state, err := readGroup(primaryGroup)
		if err != nil {
			return err
		}
		if state == nil {
			return fmt.Errorf("group %q does not exist", primaryGroup)
		}
	}
	for _, group := range plan.GetStringList("groups") {
		state, err := readGroup(group)
		if err != nil {
			return err
		}
		if state == nil {
			return fmt.Errorf("group %q does not exist", group)
		}
	}
	return nil
}

func boolWithDefault(values pluginsdk.StateData, key string, defaultValue bool) bool {
	if _, ok := values[key]; !ok {
		return defaultValue
	}
	return values.GetBool(key)
}

func groupEnsure(values pluginsdk.StateData) string {
	if values == nil {
		return "present"
	}
	if ensure := strings.TrimSpace(values.GetString("ensure")); ensure != "" {
		return ensure
	}
	return "present"
}

func userEnsure(values pluginsdk.StateData) string {
	if values == nil {
		return "present"
	}
	if ensure := strings.TrimSpace(values.GetString("ensure")); ensure != "" {
		return ensure
	}
	return "present"
}

func absentGroupState(values pluginsdk.StateData) pluginsdk.StateData {
	name := values.GetString("name")
	state := pluginsdk.StateData{
		"id":     name,
		"name":   name,
		"ensure": "absent",
	}
	if values.GetBool("system") {
		state["system"] = true
	}
	return state
}

func absentUserState(values pluginsdk.StateData) pluginsdk.StateData {
	name := values.GetString("name")
	state := pluginsdk.StateData{
		"id":     name,
		"name":   name,
		"ensure": "absent",
	}
	if _, ok := values["remove_home"]; ok {
		state["remove_home"] = values.GetBool("remove_home")
	}
	return state
}

func init() {
	pluginsdk.RegisterResource(&groupResource{})
	pluginsdk.RegisterResource(&userResource{})
}

func main() {}
