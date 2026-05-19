package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
	systemdcontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/systemd"
	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/hostfs"
)

const unitDir = "/etc/systemd/system"

type systemdUnitResource struct{}

type timezoneResource struct{}

type restartProcessAction struct{}

type systemdUnitInfoDataSource struct{}

func (r *systemdUnitResource) Name() string { return "systemd_unit" }

func (r *timezoneResource) Name() string { return "timezone" }

func (r *systemdUnitResource) Schema() pluginsdk.Schema {
	return systemdcontract.UnitResourceSchema()
}

func (r *timezoneResource) Schema() pluginsdk.Schema {
	return systemdcontract.TimezoneResourceSchema()
}

func (r *systemdUnitResource) Validate(config pluginsdk.StateData) error {
	if err := ensureSystemdAvailable(); err != nil {
		return err
	}
	name := config.GetString("name")
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("name must not be empty")
	}
	if st := config.GetString("state"); st != "" && st != "running" && st != "stopped" {
		return fmt.Errorf("state must be \"running\" or \"stopped\", got %q", st)
	}
	if config.GetBool("masked") && config.GetString("state") == "running" {
		return fmt.Errorf("masked units cannot have state \"running\"")
	}
	if content := strings.TrimSpace(config.GetString("content")); content != "" {
		if err := validateSystemdUnitContent(name, content); err != nil {
			return err
		}
	}
	return nil
}

func (r *timezoneResource) Validate(config pluginsdk.StateData) error {
	if err := ensureTimedatectlAvailable("timezone"); err != nil {
		return err
	}
	zone := strings.TrimSpace(config.GetString("zone"))
	if zone == "" {
		return fmt.Errorf("zone must not be empty")
	}
	exists, err := timezoneExists(zone)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("timezone %q is not available on this host", zone)
	}
	return nil
}

func (r *systemdUnitResource) Read(state pluginsdk.StateData) (pluginsdk.StateData, error) {
	name := state.GetString("name")
	unitName := serviceName(name)
	unitPath := unitFilePath(name)
	exists, err := pluginsdk.FileExists(unitPath)
	if err != nil {
		return nil, fmt.Errorf("check unit file: %w", err)
	}
	status, err := systemctlShow(unitName)
	if err != nil {
		return nil, err
	}
	loadState := status["LoadState"]
	activeState := status["ActiveState"]
	unitFileState := status["UnitFileState"]
	if !exists && loadState == "not-found" {
		return nil, nil
	}
	out := pluginsdk.StateData{
		"id":               name,
		"name":             name,
		"exists":           true,
		"load_state":       loadState,
		"active_state":     activeState,
		"unit_file_state":  unitFileState,
		"enabled":          isEnabledUnitState(unitFileState),
		"masked":           isMaskedUnitState(unitFileState) || loadState == "masked",
		"state":            desiredStateFromActiveState(activeState),
		"reload_on_change": systemdUnitReloadOnChange(state),
	}
	if exists {
		data, err := pluginsdk.FileRead(unitPath)
		if err != nil {
			return nil, fmt.Errorf("read unit file: %w", err)
		}
		out["content"] = string(data)
	}
	if triggers := state.GetStringList("reload_triggers"); len(triggers) > 0 {
		out["reload_triggers"] = triggers
	}

	return out, nil
}

func (r *timezoneResource) Read(_ pluginsdk.StateData) (pluginsdk.StateData, error) {
	zone, err := currentTimezone()
	if err != nil {
		return nil, err
	}
	return pluginsdk.StateData{
		"id":   "timezone",
		"zone": zone,
	}, nil
}

func (r *systemdUnitResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	name := plan.GetString("name")
	unitName := serviceName(name)
	contentChanged := false
	if content := plan.GetString("content"); content != "" {
		existingContent, err := pluginsdk.FileRead(unitFilePath(name))
		if err == nil && string(existingContent) == content {
			contentChanged = false
		} else {
			contentChanged = true
		}
		if contentChanged {
			if err := writeUnitFile(name, content); err != nil {
				return nil, err
			}
			if err := daemonReload(); err != nil {
				return nil, err
			}
		}
	}
	if plan.GetBool("enabled") {
		if err := systemctl("enable", unitName); err != nil {
			return nil, fmt.Errorf("enable %s: %w", unitName, err)
		}
	}
	if plan.GetBool("masked") {
		if err := systemctl("mask", unitName); err != nil {
			return nil, fmt.Errorf("mask %s: %w", unitName, err)
		}
	}
	if plan.GetString("state") == "running" {
		if err := systemctl("start", unitName); err != nil {
			return nil, fmt.Errorf("start %s: %w", unitName, err)
		}
	}
	return r.Read(plan)
}

func (r *timezoneResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applyTimezone(plan)
}

func (r *systemdUnitResource) Update(prior, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	name := plan.GetString("name")
	unitName := serviceName(name)
	forceReload := systemdReloadTriggerDigest(prior) != systemdReloadTriggerDigest(plan)
	if plan.GetString("content") != prior.GetString("content") {
		if content := plan.GetString("content"); content != "" {
			if err := writeUnitFile(name, content); err != nil {
				return nil, err
			}
		}
		if err := daemonReload(); err != nil {
			return nil, err
		}
	}
	if plan.GetBool("enabled") != prior.GetBool("enabled") {
		action := "disable"
		if plan.GetBool("enabled") {
			action = "enable"
		}
		if err := systemctl(action, unitName); err != nil {
			return nil, fmt.Errorf("%s %s: %w", action, unitName, err)
		}
	}
	if plan.GetBool("masked") != prior.GetBool("masked") {
		action := "unmask"
		if plan.GetBool("masked") {
			action = "mask"
		}
		if err := systemctl(action, unitName); err != nil {
			return nil, fmt.Errorf("%s %s: %w", action, unitName, err)
		}
	}
	if plan.GetString("state") != prior.GetString("state") {
		action := "stop"
		if plan.GetString("state") == "running" {
			action = "start"
		}
		if err := systemctl(action, unitName); err != nil {
			return nil, fmt.Errorf("%s %s: %w", action, unitName, err)
		}
	}
	if systemdUnitReloadOnChange(plan) && forceReload {
		status, err := systemctlShow(unitName)
		if err != nil {
			return nil, err
		}
		if desiredStateFromActiveState(status["ActiveState"]) == "running" {
			if err := systemctl("reload", unitName); err != nil {
				return nil, fmt.Errorf("reload %s: %w", unitName, err)
			}
		}
	}
	return r.Read(plan)
}

func (r *timezoneResource) Update(_ pluginsdk.StateData, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applyTimezone(plan)
}

func (r *systemdUnitResource) Delete(state pluginsdk.StateData) error {
	name := state.GetString("name")
	unitName := serviceName(name)
	if state.GetBool("masked") {
		_, _ = pluginsdk.CmdExec("systemctl", []string{"unmask", unitName})
	}
	_, _ = pluginsdk.CmdExec("systemctl", []string{"stop", unitName})
	_, _ = pluginsdk.CmdExec("systemctl", []string{"disable", unitName})
	if state.GetString("content") != "" {
		_ = pluginsdk.FileDelete(unitFilePath(name))
		_ = daemonReload()
	}
	return nil
}

func (r *timezoneResource) Delete(pluginsdk.StateData) error {
	return nil
}

func (r *systemdUnitResource) ImportState(id string) (pluginsdk.StateData, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("import ID must be a unit name, got empty string")
	}
	return r.Read(pluginsdk.StateData{"name": id})
}

func (r *timezoneResource) ImportState(id string) (pluginsdk.StateData, error) {
	trimmed := strings.TrimSpace(id)
	if trimmed != "" && trimmed != "system" && trimmed != "host" && trimmed != "timezone" {
		return nil, fmt.Errorf("import ID must be empty, \"system\", \"host\", or \"timezone\", got %q", id)
	}
	return r.Read(nil)
}

func (a *restartProcessAction) Name() string { return "restart_process" }

func (a *restartProcessAction) InputSchema() pluginsdk.Schema {
	return systemdcontract.RestartProcessActionSchema()
}

func (d *systemdUnitInfoDataSource) Name() string { return "systemd_unit_info" }

func (d *systemdUnitInfoDataSource) DataSchema() pluginsdk.Schema {
	return systemdcontract.UnitInfoDataSourceSchema()
}

func (d *systemdUnitInfoDataSource) DataRead(config pluginsdk.StateData) (pluginsdk.StateData, error) {
	if err := ensureSystemdAvailable(); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(config.GetString("name"))
	if name == "" {
		return nil, fmt.Errorf("name must not be empty")
	}

	unitName := serviceName(name)
	status, err := systemctlShow(unitName)
	if err != nil {
		return nil, err
	}

	loadState := status["LoadState"]
	activeState := status["ActiveState"]
	subState := status["SubState"]
	unitFileState := status["UnitFileState"]

	return pluginsdk.StateData{
		"id":              unitName,
		"name":            unitName,
		"load_state":      loadState,
		"active_state":    activeState,
		"sub_state":       subState,
		"unit_file_state": unitFileState,
		"enabled":         isEnabledUnitState(unitFileState),
		"masked":          isMaskedUnitState(unitFileState) || loadState == "masked",
	}, nil
}

func (a *restartProcessAction) Invoke(config pluginsdk.StateData) (pluginsdk.StateData, error) {
	name := strings.TrimSpace(config.GetString("name"))
	if name == "" {
		return nil, fmt.Errorf("name must not be empty")
	}

	command := strings.TrimSpace(config.GetString("command"))
	if command != "" {
		return pluginsdk.StateData{"name": "sh", "args": []string{"-lc", command}}, nil
	}

	manager := strings.TrimSpace(config.GetString("manager"))
	if manager == "" || manager == "auto" {
		resolved, err := resolveRestartManager(name)
		if err != nil {
			return nil, err
		}
		manager = resolved
	}

	switch manager {
	case "systemd":
		if err := ensureSystemdUnitExists(name); err != nil {
			return nil, err
		}
		return pluginsdk.StateData{"name": "systemctl", "args": []string{"restart", serviceName(name)}}, nil
	case "service":
		hasService, err := pluginsdk.HostHasCommand("service")
		if err != nil || !hasService {
			return nil, fmt.Errorf("service manager requested for %q but service command is unavailable", name)
		}
		return pluginsdk.StateData{"name": "service", "args": []string{name, "restart"}}, nil
	default:
		return nil, fmt.Errorf("no restart command resolved for %q; set command explicitly or use a supported manager", name)
	}
}

func serviceName(name string) string {
	if strings.Contains(name, ".") {
		return name
	}
	return name + ".service"
}

func unitFilePath(name string) string {
	return unitDir + "/" + serviceName(name)
}

func writeUnitFile(name, content string) error {
	path := unitFilePath(name)
	if err := pluginsdk.FileWrite(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("write unit file %s: %w", path, err)
	}
	return nil
}

func daemonReload() error {
	res, err := pluginsdk.CmdExec("systemctl", []string{"daemon-reload"})
	if err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("daemon-reload failed (exit %d): %s", res.ExitCode, res.Stderr)
	}
	return nil
}

func systemctlShow(unit string) (map[string]string, error) {
	result, err := pluginsdk.CmdExec("systemctl", []string{"show", "--property", "LoadState,ActiveState,SubState,UnitFileState", unit})
	if err != nil {
		return nil, fmt.Errorf("systemctl show %s: %w", unit, err)
	}
	status := map[string]string{}
	for _, line := range strings.Split(result.Stdout, "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		status[parts[0]] = parts[1]
	}
	if _, ok := status["LoadState"]; !ok {
		status["LoadState"] = "not-found"
	}
	if _, ok := status["ActiveState"]; !ok {
		status["ActiveState"] = "inactive"
	}
	if _, ok := status["SubState"]; !ok {
		status["SubState"] = ""
	}
	if _, ok := status["UnitFileState"]; !ok {
		status["UnitFileState"] = ""
	}
	return status, nil
}

func isEnabledUnitState(state string) bool {
	switch state {
	case "enabled", "enabled-runtime", "linked", "linked-runtime", "alias":
		return true
	default:
		return false
	}
}

func isMaskedUnitState(state string) bool {
	return state == "masked" || state == "masked-runtime"
}

func desiredStateFromActiveState(activeState string) string {
	switch activeState {
	case "active", "activating", "reloading":
		return "running"
	default:
		return "stopped"
	}
}

func systemdUnitReloadOnChange(values pluginsdk.StateData) bool {
	if _, ok := values["reload_on_change"]; !ok {
		return false
	}
	return values.GetBool("reload_on_change")
}

func systemdReloadTriggerDigest(values pluginsdk.StateData) string {
	triggers := normalizeSystemdStringList(values.GetStringList("reload_triggers"))
	return strings.Join(triggers, "\x00")
}

func normalizeSystemdStringList(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		set[value] = true
	}
	if len(set) == 0 {
		return nil
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func ensureSystemdUnitExists(name string) error {
	status, err := systemctlShow(serviceName(name))
	if err != nil {
		return err
	}
	if status["LoadState"] == "not-found" {
		return fmt.Errorf("systemd unit %q was not found", serviceName(name))
	}
	return nil
}

func resolveRestartManager(name string) (string, error) {
	hasSystemctl, err := pluginsdk.HostHasCommand("systemctl")
	if err == nil && hasSystemctl {
		if ensureErr := ensureSystemdUnitExists(name); ensureErr == nil {
			return "systemd", nil
		}
	}
	hasService, serviceErr := pluginsdk.HostHasCommand("service")
	if serviceErr == nil && hasService {
		return "service", nil
	}
	return "", fmt.Errorf("no restart manager resolved for %q", name)
}

func applyTimezone(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	zone := strings.TrimSpace(plan.GetString("zone"))
	if err := setTimezone(zone); err != nil {
		return nil, err
	}
	return (&timezoneResource{}).Read(nil)
}

func currentTimezone() (string, error) {
	value, err := timedatectlShowValue("Timezone")
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf("timedatectl returned an empty timezone")
	}
	return value, nil
}

func setTimezone(zone string) error {
	res, err := pluginsdk.CmdExec("timedatectl", []string{"set-timezone", zone})
	if err != nil {
		return fmt.Errorf("timedatectl set-timezone %q: %w", zone, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("timedatectl set-timezone %q failed (%s)", zone, pluginsdk.CommandFailureDetail(res))
	}
	return nil
}

func timedatectlShowValue(property string) (string, error) {
	res, err := pluginsdk.CmdExec("timedatectl", []string{"show", "--property=" + property, "--value"})
	if err != nil {
		return "", fmt.Errorf("timedatectl show %s: %w", property, err)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("timedatectl show %s failed (%s)", property, pluginsdk.CommandFailureDetail(res))
	}
	return strings.TrimSpace(res.Stdout), nil
}

func timezoneExists(zone string) (bool, error) {
	res, err := pluginsdk.CmdExec("timedatectl", []string{"list-timezones"})
	if err != nil {
		return false, fmt.Errorf("timedatectl list-timezones: %w", err)
	}
	if res.ExitCode != 0 {
		return false, fmt.Errorf("timedatectl list-timezones failed (%s)", pluginsdk.CommandFailureDetail(res))
	}
	for _, candidate := range strings.Split(res.Stdout, "\n") {
		if strings.TrimSpace(candidate) == zone {
			return true, nil
		}
	}
	return false, nil
}

func ensureSystemdAvailable() error {
	return ensureCommandAvailable("systemctl", "systemd_unit")
}

func ensureTimedatectlAvailable(feature string) error {
	return ensureCommandAvailable("timedatectl", feature)
}

func ensureCommandAvailable(name, feature string) error {
	hasCommand, err := pluginsdk.HostHasCommand(name)
	if err != nil {
		return fmt.Errorf("check %s command: %w", name, err)
	}
	if !hasCommand {
		return fmt.Errorf("%s requires %s", feature, name)
	}
	return nil
}

func validateSystemdUnitContent(name, content string) error {
	hasVerify, err := pluginsdk.HostHasCommand("systemd-analyze")
	if err != nil {
		return fmt.Errorf("check systemd-analyze command: %w", err)
	}
	if !hasVerify {
		return fmt.Errorf("systemd_unit content validation requires systemd-analyze")
	}

	unitName := serviceName(name)
	ext := filepath.Ext(unitName)
	if ext == "" {
		ext = ".service"
	}
	baseName := strings.TrimSuffix(unitName, ext)
	tmpPath, err := hostfs.WriteTempFile("tf-nix-systemd-validate-"+sanitizeValidationName(baseName), ext, []byte(content), 0o644)
	if err != nil {
		return err
	}
	defer func() {
		_ = hostfs.CleanupFile(tmpPath)
	}()

	res, err := pluginsdk.CmdExec("systemd-analyze", []string{"verify", tmpPath})
	if err != nil {
		return fmt.Errorf("systemd-analyze verify %s: %w", tmpPath, err)
	}
	if res.ExitCode != 0 {
		detail := strings.TrimSpace(res.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(res.Stdout)
		}
		return fmt.Errorf("systemd-analyze verify failed (exit %d): %s", res.ExitCode, detail)
	}
	return nil
}

func sanitizeValidationName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unit"
	}

	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}

	sanitized := strings.Trim(b.String(), "-")
	if sanitized == "" {
		return "unit"
	}
	return sanitized
}

func systemctl(action, unit string) error {
	res, err := pluginsdk.CmdExec("systemctl", []string{action, unit})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("systemctl %s %s failed (exit %d): %s", action, unit, res.ExitCode, res.Stderr)
	}
	return nil
}

func init() {
	pluginsdk.RegisterResource(&systemdUnitResource{})
	pluginsdk.RegisterResource(&timezoneResource{})
	pluginsdk.RegisterDataSource(&systemdUnitInfoDataSource{})
	pluginsdk.RegisterAction(&restartProcessAction{})
}

func main() {}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
