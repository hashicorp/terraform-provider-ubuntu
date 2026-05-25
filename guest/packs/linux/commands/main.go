// Copyright IBM Corp. 2026

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
	linuxcommandscontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/linuxcommands"
)

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type commandResource struct{}
type runCommandAction struct{}

func (r *commandResource) Name() string { return "command" }

func (r *commandResource) Schema() pluginsdk.Schema {
	return linuxcommandscontract.CommandResourceSchema()
}

func (r *commandResource) Validate(config pluginsdk.StateData) error {
	return validateCommandConfig(config, true)
}

func validateCommandConfig(config pluginsdk.StateData, requireGuard bool) error {
	name := strings.TrimSpace(config.GetString("name"))
	if name == "" {
		return fmt.Errorf("name must not be empty")
	}
	command := strings.TrimSpace(config.GetString("command"))
	if command == "" {
		return fmt.Errorf("command must not be empty")
	}
	creates := strings.TrimSpace(config.GetString("creates"))
	unless := strings.TrimSpace(config.GetString("unless"))
	if requireGuard && creates == "" && unless == "" {
		return fmt.Errorf("at least one of creates or unless must be set")
	}
	if creates != "" && !filepath.IsAbs(creates) {
		return fmt.Errorf("creates must be an absolute path, got %q", creates)
	}
	workingDirectory := strings.TrimSpace(config.GetString("working_directory"))
	if workingDirectory != "" && !filepath.IsAbs(workingDirectory) {
		return fmt.Errorf("working_directory must be an absolute path, got %q", workingDirectory)
	}
	interpreter := config.GetStringList("interpreter")
	if len(interpreter) == 1 && strings.TrimSpace(interpreter[0]) == "" {
		return fmt.Errorf("interpreter entries must not be empty")
	}
	for _, arg := range interpreter {
		if strings.TrimSpace(arg) == "" {
			return fmt.Errorf("interpreter entries must not be empty")
		}
	}
	for key := range config.GetMap("environment") {
		if !envNamePattern.MatchString(key) {
			return fmt.Errorf("environment variable names must match %s, got %q", envNamePattern.String(), key)
		}
	}
	for key := range config.GetMap("triggers") {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("triggers must not contain empty keys")
		}
	}
	return nil
}

func (r *commandResource) Read(state pluginsdk.StateData) (pluginsdk.StateData, error) {
	if state == nil {
		return nil, nil
	}

	present, err := guardSatisfied(state)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}

	return stateWithExistingResult(state), nil
}

func (r *commandResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	present, err := guardSatisfied(plan)
	if err != nil {
		return nil, err
	}
	if present {
		return stateWithResult(nil, plan, &pluginsdk.CmdResult{}), nil
	}

	result, err := runManagedCommand(plan, plan.GetString("command"))
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, commandFailure("command", result)
	}

	present, err = guardSatisfied(plan)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, fmt.Errorf("command completed successfully but neither creates nor unless is now satisfied")
	}

	return stateWithResult(nil, plan, result), nil
}

func (r *commandResource) Update(state pluginsdk.StateData, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	result, err := runManagedCommand(plan, plan.GetString("command"))
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, commandFailure("command", result)
	}

	present, err := guardSatisfied(plan)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, fmt.Errorf("command completed successfully but neither creates nor unless is now satisfied")
	}

	return stateWithResult(state, plan, result), nil
}

func (r *commandResource) Delete(state pluginsdk.StateData) error {
	deleteCommand := strings.TrimSpace(state.GetString("delete_command"))
	if deleteCommand == "" {
		return nil
	}

	result, err := runManagedCommand(state, deleteCommand)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return commandFailure("delete_command", result)
	}
	return nil
}

func (r *commandResource) ImportState(_ string) (pluginsdk.StateData, error) {
	return nil, fmt.Errorf("command resources cannot be imported")
}

func guardSatisfied(config pluginsdk.StateData) (bool, error) {
	creates := strings.TrimSpace(config.GetString("creates"))
	if creates != "" {
		stat, err := pluginsdk.FileStat_(creates)
		if err != nil {
			if !isNotExistError(err) {
				return false, fmt.Errorf("check creates path %q: %w", creates, err)
			}
		} else if stat != nil {
			return true, nil
		}
	}

	unless := strings.TrimSpace(config.GetString("unless"))
	if unless == "" {
		return false, nil
	}

	result, err := runManagedCommand(config, unless)
	if err != nil {
		return false, fmt.Errorf("run unless guard: %w", err)
	}
	return result.ExitCode == 0, nil
}

func isNotExistError(err error) bool {
	if err == nil {
		return false
	}
	if os.IsNotExist(err) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "no such file or directory")
}

func runManagedCommand(config pluginsdk.StateData, script string) (*pluginsdk.CmdResult, error) {
	script = strings.TrimSpace(script)
	if script == "" {
		return nil, fmt.Errorf("shell snippet must not be empty")
	}

	interpreter := normalizedInterpreter(config)
	cmd := interpreter[0]
	args := append(cloneStringList(interpreter[1:]), wrapShellScript(script, strings.TrimSpace(config.GetString("working_directory")), config.GetMap("environment")))

	pluginsdk.LogInfo(fmt.Sprintf("running managed command %q via %s", config.GetString("name"), cmd))
	result, err := pluginsdk.CmdExec(cmd, args)
	if err != nil {
		return nil, fmt.Errorf("execute %q: %w", config.GetString("name"), err)
	}
	return result, nil
}

func stateWithResult(prior pluginsdk.StateData, config pluginsdk.StateData, result *pluginsdk.CmdResult) pluginsdk.StateData {
	state := pluginsdk.StateData{
		"id":          config.GetString("name"),
		"name":        config.GetString("name"),
		"command":     config.GetString("command"),
		"interpreter": normalizedInterpreter(config),
		"stdout":      "",
		"stderr":      "",
		"exit_code":   0,
	}

	preserveNullableString(state, prior, config, "creates")
	preserveNullableString(state, prior, config, "unless")
	preserveNullableString(state, prior, config, "delete_command")
	if workingDirectory := config.GetString("working_directory"); workingDirectory != "" {
		state["working_directory"] = workingDirectory
	}
	if runAs := config.GetString("run_as"); runAs != "" {
		state["run_as"] = runAs
	}
	if environment := cloneStringMap(config.GetMap("environment")); len(environment) > 0 {
		state["environment"] = environment
	}
	if triggers := cloneStringMap(config.GetMap("triggers")); len(triggers) > 0 {
		state["triggers"] = triggers
	}

	if result != nil {
		state["stdout"] = result.Stdout
		state["stderr"] = result.Stderr
		state["exit_code"] = result.ExitCode
	}

	return state
}

func stateWithExistingResult(config pluginsdk.StateData) pluginsdk.StateData {
	var result *pluginsdk.CmdResult
	if _, ok := config["stdout"]; ok || config.GetString("stderr") != "" || config.GetInt("exit_code") != 0 {
		result = &pluginsdk.CmdResult{
			Stdout:   config.GetString("stdout"),
			Stderr:   config.GetString("stderr"),
			ExitCode: config.GetInt("exit_code"),
		}
	}
	return stateWithResult(config, config, result)
}

func preserveNullableString(state pluginsdk.StateData, prior pluginsdk.StateData, config pluginsdk.StateData, key string) {
	if value, ok := config[key]; ok {
		text, ok := value.(string)
		if !ok || text == "" {
			state[key] = nil
			return
		}
		state[key] = text
		return
	}

	if prior != nil {
		if _, ok := prior[key]; ok {
			state[key] = nil
		}
	}
}

func normalizedInterpreter(config pluginsdk.StateData) []string {
	interpreter := config.GetStringList("interpreter")
	if len(interpreter) == 0 {
		return []string{"sh", "-lc"}
	}
	return cloneStringList(interpreter)
}

func wrapShellScript(script, workingDirectory string, environment map[string]string) string {
	parts := make([]string, 0, 3)
	if workingDirectory != "" {
		parts = append(parts, "cd -- "+shellQuote(workingDirectory))
	}
	if len(environment) > 0 {
		keys := make([]string, 0, len(environment))
		for key := range environment {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		assignments := make([]string, 0, len(keys))
		for _, key := range keys {
			assignments = append(assignments, key+"="+shellQuote(environment[key]))
		}
		parts = append(parts, "export "+strings.Join(assignments, " "))
	}
	parts = append(parts, script)
	return strings.Join(parts, " && ")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func cloneStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func commandFailure(field string, result *pluginsdk.CmdResult) error {
	message := strings.TrimSpace(result.Stderr)
	if message == "" {
		message = strings.TrimSpace(result.Stdout)
	}
	if message == "" {
		message = "command failed"
	}
	return fmt.Errorf("%s failed (exit %d): %s", field, result.ExitCode, message)
}

func (a *runCommandAction) Name() string { return "run_command" }

func (a *runCommandAction) InputSchema() pluginsdk.Schema {
	return linuxcommandscontract.RunCommandActionSchema()
}

func (a *runCommandAction) Invoke(config pluginsdk.StateData) (pluginsdk.StateData, error) {
	if err := validateCommandConfig(config, false); err != nil {
		return nil, err
	}

	result, err := runManagedCommand(config, config.GetString("command"))
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, commandFailure("command", result)
	}

	return pluginsdk.StateData{
		"stdout":    result.Stdout,
		"stderr":    result.Stderr,
		"exit_code": result.ExitCode,
	}, nil
}

func init() {
	pluginsdk.RegisterResource(&commandResource{})
	pluginsdk.RegisterAction(&runCommandAction{})
}

func main() {
	pluginsdk.Run()
}
