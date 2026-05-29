// Copyright IBM Corp. 2026

package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	pluginsdk "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
	systemdcontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/systemd"
	timesynccontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/timesync"
	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/hostfs"
)

const (
	unitDir                           = "/etc/systemd/system"
	serviceIdentityDropInFile         = "terraform-service-identity.override.conf"
	runtimeUnitDir                    = "/run/systemd/system"
	localUnitDir                      = "/usr/local/lib/systemd/system"
	usrUnitDir                        = "/usr/lib/systemd/system"
	libUnitDir                        = "/lib/systemd/system"
	timesyncdConfigDir                = "/etc/systemd/timesyncd.conf.d"
	timesyncdConfigPath               = "/etc/systemd/timesyncd.conf.d/90-tf-linux-provider-timesync.conf"
	timesyncdServiceName              = "systemd-timesyncd.service"
	timesyncBackendName               = "systemd-timesyncd"
	serviceIdentityDefaultProvider    = "terraform_provider_linux"
	serviceIdentityManagedLabelPrefix = "# Managed by "
	serviceIdentityManagedLabelSuffix = ". Changes will be overwritten."
)

type systemdUnitResource struct{}

type serviceIdentityDropInChange struct {
	path            string
	previousContent []byte
	previousExists  bool
	changed         bool
}

type timezoneResource struct{}

type timesyncResource struct{}

type timesyncSpec struct {
	Enabled         bool
	Servers         []string
	FallbackServers []string
}

type restartProcessAction struct{}

type systemdUnitInfoDataSource struct{}

func (r *systemdUnitResource) Name() string { return "systemd_unit" }

func (r *timezoneResource) Name() string { return "timezone" }

func (r *timesyncResource) Name() string { return "timesync" }

func (r *systemdUnitResource) Schema() pluginsdk.Schema {
	return systemdcontract.UnitResourceSchema()
}

func (r *timezoneResource) Schema() pluginsdk.Schema {
	return systemdcontract.TimezoneResourceSchema()
}

func (r *timesyncResource) Schema() pluginsdk.Schema {
	return timesynccontract.ResourceSchema()
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
	if err := validateServiceIdentityConfig(config); err != nil {
		return err
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

func (r *timesyncResource) Validate(config pluginsdk.StateData) error {
	if err := ensureTimesyncdAvailable(); err != nil {
		return err
	}
	_, err := desiredTimesyncSpec(config)
	return err
}

func (r *systemdUnitResource) Read(state pluginsdk.StateData) (pluginsdk.StateData, error) {
	name := state.GetString("name")
	unitName := serviceName(name)
	unitPath := unitFilePath(name)
	dropInPath := serviceIdentityDropInPath(name)
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
	dropInExists, err := pluginsdk.FileExists(dropInPath)
	if err != nil {
		return nil, fmt.Errorf("check service identity drop-in %s: %w", dropInPath, err)
	}
	if !dropInExists && hasServiceIdentityState(state) {
		return nil, fmt.Errorf("service identity drop-in %s is missing; remove service_user/service_group from configuration or restore the Terraform-managed file", dropInPath)
	}
	if dropInExists {
		data, err := pluginsdk.FileRead(dropInPath)
		if err != nil {
			return nil, fmt.Errorf("read service identity drop-in %s: %w", dropInPath, err)
		}
		if err := validateServiceIdentityDropInMatchesState(dropInPath, data, state); err != nil {
			return nil, err
		}
		user, group := parseServiceIdentityDropIn(string(data))
		if user != "" {
			out["service_user"] = user
		}
		if group != "" {
			out["service_group"] = group
		}
		out["service_identity_dropin_path"] = dropInPath
	}
	if triggers := state.GetStringList("reload_triggers"); len(triggers) > 0 {
		out["reload_triggers"] = triggers
	}
	if err := validateNoLaterServiceIdentityDropIns(name, state); err != nil {
		return nil, err
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

func (r *timesyncResource) Read(_ pluginsdk.StateData) (pluginsdk.StateData, error) {
	if err := ensureTimesyncdAvailable(); err != nil {
		return nil, err
	}
	status, err := systemctlShow(timesyncdServiceName)
	if err != nil {
		return nil, err
	}
	servers, fallbackServers, err := readTimesyncdConfig()
	if err != nil {
		return nil, err
	}
	return pluginsdk.StateData{
		"id":               "timesync",
		"enabled":          timesyncdEnabled(status),
		"servers":          servers,
		"fallback_servers": fallbackServers,
		"backend":          timesyncBackendName,
		"config_path":      timesyncdConfigPath,
		"service_name":     timesyncdServiceName,
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
		}
	}
	identityChange, err := applyServiceIdentityDropIn(name, plan, nil)
	if err != nil {
		return nil, err
	}
	if contentChanged || identityChange.changed {
		if err := daemonReload(); err != nil {
			return nil, rollbackServiceIdentityDropInAfterError(identityChange, err)
		}
	}
	if err := validateEffectiveServiceIdentity(unitName, plan); err != nil {
		return nil, rollbackServiceIdentityDropInAfterError(identityChange, err)
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
	if err := reconcileCreateServiceState(unitName, plan, identityChange.changed); err != nil {
		return nil, err
	}
	return r.Read(plan)
}

func (r *timezoneResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applyTimezone(plan)
}

func (r *timesyncResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applyTimesync(plan)
}

func (r *systemdUnitResource) Update(prior, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	name := plan.GetString("name")
	unitName := serviceName(name)
	forceReload := systemdReloadTriggerDigest(prior) != systemdReloadTriggerDigest(plan)
	contentChanged := false
	if plan.GetString("content") != prior.GetString("content") {
		if content := plan.GetString("content"); content != "" {
			if err := writeUnitFile(name, content); err != nil {
				return nil, err
			}
		} else if prior.GetString("content") != "" {
			if err := pluginsdk.FileDelete(unitFilePath(name)); err != nil {
				return nil, fmt.Errorf("delete unit file %s: %w", unitFilePath(name), err)
			}
		}
		contentChanged = true
	}
	identityChange, err := applyServiceIdentityDropIn(name, plan, prior)
	if err != nil {
		return nil, err
	}
	if contentChanged || identityChange.changed {
		if err := daemonReload(); err != nil {
			return nil, rollbackServiceIdentityDropInAfterError(identityChange, err)
		}
	}
	if err := validateEffectiveServiceIdentity(unitName, plan); err != nil {
		return nil, rollbackServiceIdentityDropInAfterError(identityChange, err)
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
	if err := reconcileUpdateServiceState(unitName, prior, plan, identityChange.changed); err != nil {
		return nil, err
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

func (r *timesyncResource) Update(_ pluginsdk.StateData, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applyTimesync(plan)
}

func (r *systemdUnitResource) Delete(state pluginsdk.StateData) error {
	name := state.GetString("name")
	unitName := serviceName(name)
	var identityDropIn serviceIdentityDropInChange
	var removeIdentityDropIn bool
	if hasServiceIdentityState(state) {
		change, err := captureServiceIdentityDropIn(serviceIdentityDropInPath(name))
		if err != nil {
			return err
		}
		if !change.previousExists {
			return fmt.Errorf("service identity drop-in %s is missing; remove service_user/service_group from configuration or restore the Terraform-managed file", change.path)
		}
		if err := validateServiceIdentityDropInMatchesState(change.path, change.previousContent, state); err != nil {
			return err
		}
		identityDropIn = change
		removeIdentityDropIn = true
	}
	if state.GetBool("masked") {
		_, _ = pluginsdk.CmdExec("systemctl", []string{"unmask", unitName})
	}
	_, _ = pluginsdk.CmdExec("systemctl", []string{"stop", unitName})
	_, _ = pluginsdk.CmdExec("systemctl", []string{"disable", unitName})
	if removeIdentityDropIn {
		changed, err := deleteCapturedServiceIdentityDropIn(identityDropIn)
		if err != nil {
			return err
		}
		if changed {
			if err := daemonReload(); err != nil {
				return err
			}
		}
	}
	if state.GetString("content") != "" {
		_ = pluginsdk.FileDelete(unitFilePath(name))
		_ = daemonReload()
	}
	return nil
}

func (r *timezoneResource) Delete(pluginsdk.StateData) error {
	return nil
}

func (r *timesyncResource) Delete(pluginsdk.StateData) error {
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

func (r *timesyncResource) ImportState(id string) (pluginsdk.StateData, error) {
	trimmed := strings.TrimSpace(id)
	if trimmed != "" && trimmed != "system" && trimmed != "host" && trimmed != "timesync" {
		return nil, fmt.Errorf("import ID must be empty, \"system\", \"host\", or \"timesync\", got %q", id)
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

func serviceIdentityDropInDir(name string) string {
	return unitDir + "/" + serviceName(name) + ".d"
}

func serviceIdentityDropInPath(name string) string {
	return serviceIdentityDropInDir(name) + "/" + serviceIdentityDropInFile
}

func serviceIdentitySearchDropInDirs(name string) []string {
	unitName := serviceName(name)
	return []string{
		unitDir + "/" + unitName + ".d",
		runtimeUnitDir + "/" + unitName + ".d",
		localUnitDir + "/" + unitName + ".d",
		usrUnitDir + "/" + unitName + ".d",
		libUnitDir + "/" + unitName + ".d",
	}
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
	status, err := systemctlShowProperties(unit, "LoadState", "ActiveState", "SubState", "UnitFileState")
	if err != nil {
		return nil, err
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

func systemctlShowProperties(unit string, properties ...string) (map[string]string, error) {
	args := []string{"show"}
	if len(properties) > 0 {
		args = append(args, "--property", strings.Join(properties, ","))
	}
	args = append(args, unit)
	result, err := pluginsdk.CmdExec("systemctl", args)
	if err != nil {
		return nil, fmt.Errorf("systemctl show %s: %w", unit, err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("systemctl show %s failed (%s)", unit, pluginsdk.CommandFailureDetail(result))
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

func validateServiceIdentityConfig(config pluginsdk.StateData) error {
	userSet, groupSet := hasConfiguredServiceIdentity(config)
	if !userSet && !groupSet {
		return nil
	}
	if strings.TrimSpace(config.GetString("content")) != "" {
		return fmt.Errorf("content cannot be used with service_user or service_group")
	}
	unitName := serviceName(strings.TrimSpace(config.GetString("name")))
	if !strings.HasSuffix(unitName, ".service") {
		return fmt.Errorf("service_user and service_group can only be used with .service units, got %q", unitName)
	}
	if userSet {
		if err := validateServiceIdentityValue("service_user", config.GetString("service_user")); err != nil {
			return err
		}
	}
	if groupSet {
		if err := validateServiceIdentityValue("service_group", config.GetString("service_group")); err != nil {
			return err
		}
	}
	return nil
}

func hasConfiguredServiceIdentity(values pluginsdk.StateData) (bool, bool) {
	_, userSet := values["service_user"]
	_, groupSet := values["service_group"]
	return userSet, groupSet
}

func validateServiceIdentityValue(fieldName, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("%s must not be empty when set", fieldName)
	}
	if strings.ContainsAny(trimmed, " \t\r\n") {
		return fmt.Errorf("%s must not contain whitespace", fieldName)
	}
	return nil
}

func desiredServiceIdentity(values pluginsdk.StateData) (string, string, bool) {
	user := strings.TrimSpace(values.GetString("service_user"))
	group := strings.TrimSpace(values.GetString("service_group"))
	return user, group, user != "" || group != ""
}

func hasServiceIdentityState(values pluginsdk.StateData) bool {
	_, _, wanted := desiredServiceIdentity(values)
	return wanted || strings.TrimSpace(values.GetString("service_identity_dropin_path")) != ""
}

func applyServiceIdentityDropIn(name string, plan pluginsdk.StateData, prior pluginsdk.StateData) (serviceIdentityDropInChange, error) {
	user, group, wanted := desiredServiceIdentity(plan)
	path := serviceIdentityDropInPath(name)
	change, err := captureServiceIdentityDropIn(path)
	if err != nil {
		return change, err
	}
	priorHasIdentity := prior != nil && hasServiceIdentityState(prior)
	if wanted || priorHasIdentity {
		if err := validateServiceIdentityDropInForApply(change, prior); err != nil {
			return change, err
		}
	}
	if !wanted {
		if priorHasIdentity {
			if !change.previousExists {
				return change, nil
			}
			if err := pluginsdk.FileDelete(path); err != nil {
				return change, fmt.Errorf("delete service identity drop-in %s: %w", path, err)
			}
			change.changed = true
			return change, nil
		}
		return change, nil
	}
	if err := validateNoLaterServiceIdentityDropIns(name, plan); err != nil {
		return change, err
	}

	rendered := renderServiceIdentityDropIn(user, group)
	if change.previousExists && string(change.previousContent) == rendered {
		return change, nil
	}
	if err := pluginsdk.DirEnsure(serviceIdentityDropInDir(name), 0o755); err != nil {
		return change, fmt.Errorf("ensure service identity drop-in dir %s: %w", serviceIdentityDropInDir(name), err)
	}
	if err := pluginsdk.FileWrite(path, []byte(rendered), 0o644); err != nil {
		return change, fmt.Errorf("write service identity drop-in %s: %w", path, err)
	}
	change.changed = true
	return change, nil
}

func captureServiceIdentityDropIn(path string) (serviceIdentityDropInChange, error) {
	change := serviceIdentityDropInChange{path: path}
	exists, err := pluginsdk.FileExists(path)
	if err != nil {
		return change, fmt.Errorf("check service identity drop-in %s: %w", path, err)
	}
	if !exists {
		return change, nil
	}
	data, err := pluginsdk.FileRead(path)
	if err != nil {
		return change, fmt.Errorf("read service identity drop-in %s: %w", path, err)
	}
	change.previousExists = true
	change.previousContent = append([]byte(nil), data...)
	return change, nil
}

func isManagedServiceIdentityDropIn(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		return trimmed == serviceIdentityDropInManagedLabel()
	}
	return false
}

func validateServiceIdentityDropInForApply(change serviceIdentityDropInChange, prior pluginsdk.StateData) error {
	priorHasIdentity := prior != nil && hasServiceIdentityState(prior)
	if !change.previousExists {
		if priorHasIdentity {
			return fmt.Errorf("service identity drop-in %s is missing; remove service_user/service_group from configuration or restore the Terraform-managed file", change.path)
		}
		return nil
	}
	if !isManagedServiceIdentityDropIn(string(change.previousContent)) {
		return fmt.Errorf("service identity drop-in %s already exists and is not managed by %s", change.path, serviceIdentityProviderName())
	}
	if !priorHasIdentity {
		return fmt.Errorf("service identity drop-in %s already exists unexpectedly; import or remove it before configuring service_user/service_group", change.path)
	}
	return validateServiceIdentityDropInMatchesState(change.path, change.previousContent, prior)
}

func validateServiceIdentityDropInMatchesState(path string, content []byte, state pluginsdk.StateData) error {
	if !hasServiceIdentityState(state) {
		return fmt.Errorf("service identity drop-in %s exists unexpectedly; import or remove it before configuring service_user/service_group", path)
	}
	text := string(content)
	if !isManagedServiceIdentityDropIn(text) {
		return fmt.Errorf("service identity drop-in %s already exists and is not managed by %s", path, serviceIdentityProviderName())
	}
	user, group, wanted := desiredServiceIdentity(state)
	if !wanted {
		return nil
	}
	if text != renderServiceIdentityDropIn(user, group) {
		return fmt.Errorf("service identity drop-in %s was modified outside Terraform; restore it or update service_user/service_group through Terraform", path)
	}
	return nil
}

func rollbackServiceIdentityDropInAfterError(change serviceIdentityDropInChange, cause error) error {
	if !change.changed {
		return cause
	}
	if err := rollbackServiceIdentityDropIn(change); err != nil {
		return fmt.Errorf("%w; failed to roll back service identity drop-in %s: %v", cause, change.path, err)
	}
	if err := daemonReload(); err != nil {
		return fmt.Errorf("%w; rolled back service identity drop-in %s but daemon-reload failed: %v", cause, change.path, err)
	}
	return cause
}

func rollbackServiceIdentityDropIn(change serviceIdentityDropInChange) error {
	if !change.changed {
		return nil
	}
	if change.previousExists {
		if err := pluginsdk.FileWrite(change.path, change.previousContent, 0o644); err != nil {
			return fmt.Errorf("restore previous drop-in content: %w", err)
		}
		return nil
	}
	exists, err := pluginsdk.FileExists(change.path)
	if err != nil {
		return fmt.Errorf("check written drop-in: %w", err)
	}
	if !exists {
		return nil
	}
	if err := pluginsdk.FileDelete(change.path); err != nil {
		return fmt.Errorf("delete written drop-in: %w", err)
	}
	return nil
}

func deleteCapturedServiceIdentityDropIn(change serviceIdentityDropInChange) (bool, error) {
	if !change.previousExists {
		return false, nil
	}
	if err := pluginsdk.FileDelete(change.path); err != nil {
		return false, fmt.Errorf("delete service identity drop-in %s: %w", change.path, err)
	}
	return true, nil
}

func renderServiceIdentityDropIn(user, group string) string {
	lines := []string{
		serviceIdentityDropInManagedLabel(),
		"[Service]",
	}
	if user != "" {
		lines = append(lines, "User="+user)
	}
	if group != "" {
		lines = append(lines, "Group="+group)
	}
	return strings.Join(lines, "\n") + "\n"
}

func serviceIdentityDropInManagedLabel() string {
	return serviceIdentityManagedLabelPrefix + serviceIdentityProviderName() + serviceIdentityManagedLabelSuffix
}

func serviceIdentityProviderName() string {
	return normalizeServiceIdentityProviderName(pluginsdk.ProviderName())
}

func normalizeServiceIdentityProviderName(providerName string) string {
	name := strings.TrimSpace(providerName)
	if name == "" {
		return serviceIdentityDefaultProvider
	}
	name = strings.NewReplacer("-", "_", ".", "_", "/", "_").Replace(name)
	if strings.HasPrefix(name, "terraform_provider_") {
		return name
	}
	return "terraform_provider_" + name
}

func parseServiceIdentityDropIn(content string) (string, string) {
	user, group, _, _ := parseServiceIdentityDropInFields(content)
	return user, group
}

func parseServiceIdentityDropInFields(content string) (string, string, bool, bool) {
	var user string
	var group string
	var userSet bool
	var groupSet bool
	inServiceSection := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inServiceSection = trimmed == "[Service]"
			continue
		}
		if !inServiceSection {
			continue
		}
		key, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "User":
			user = strings.TrimSpace(value)
			userSet = true
		case "Group":
			group = strings.TrimSpace(value)
			groupSet = true
		}
	}
	return user, group, userSet, groupSet
}

func validateNoLaterServiceIdentityDropIns(name string, values pluginsdk.StateData) error {
	user, group, wanted := desiredServiceIdentity(values)
	if !wanted {
		return nil
	}
	for _, dir := range serviceIdentitySearchDropInDirs(name) {
		exists, err := pluginsdk.FileExists(dir)
		if err != nil {
			return fmt.Errorf("check systemd drop-in dir %s: %w", dir, err)
		}
		if !exists {
			continue
		}
		entries, err := pluginsdk.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("read systemd drop-in dir %s: %w", dir, err)
		}
		slices.SortFunc(entries, func(a, b pluginsdk.DirEntry) int {
			return strings.Compare(a.Name, b.Name)
		})
		for _, entry := range entries {
			if entry.IsDir || !strings.HasSuffix(entry.Name, ".conf") || entry.Name <= serviceIdentityDropInFile {
				continue
			}
			path := filepath.Join(dir, entry.Name)
			data, err := pluginsdk.FileRead(path)
			if err != nil {
				return fmt.Errorf("read systemd drop-in %s: %w", path, err)
			}
			_, _, userSet, groupSet := parseServiceIdentityDropInFields(string(data))
			conflicts := make([]string, 0, 2)
			if user != "" && userSet {
				conflicts = append(conflicts, "User")
			}
			if group != "" && groupSet {
				conflicts = append(conflicts, "Group")
			}
			if len(conflicts) > 0 {
				return fmt.Errorf("service identity drop-in %s is overridden by higher-priority drop-in %s setting %s; remove that drop-in or choose a non-conflicting unit before applying", serviceIdentityDropInPath(name), path, strings.Join(conflicts, ", "))
			}
		}
	}
	return nil
}

func validateEffectiveServiceIdentity(unitName string, values pluginsdk.StateData) error {
	user, group, wanted := desiredServiceIdentity(values)
	if !wanted {
		return nil
	}
	status, err := systemctlShowProperties(unitName, "User", "Group", "DropInPaths")
	if err != nil {
		return fmt.Errorf("inspect effective service identity for %s: %w", unitName, err)
	}
	mismatches := make([]string, 0, 2)
	if user != "" && status["User"] != user {
		mismatches = append(mismatches, fmt.Sprintf("User=%q (want %q)", status["User"], user))
	}
	if group != "" && status["Group"] != group {
		mismatches = append(mismatches, fmt.Sprintf("Group=%q (want %q)", status["Group"], group))
	}
	if len(mismatches) == 0 {
		return nil
	}
	detail := ""
	if dropIns := strings.TrimSpace(status["DropInPaths"]); dropIns != "" {
		detail = "; loaded drop-ins: " + dropIns
	}
	return fmt.Errorf("systemd resolved service identity for %s as %s after loading %s; another drop-in may override the provider-managed service identity%s", unitName, strings.Join(mismatches, ", "), serviceIdentityDropInPath(unitName), detail)
}

func reconcileCreateServiceState(unitName string, plan pluginsdk.StateData, identityChanged bool) error {
	switch plan.GetString("state") {
	case "running":
		return ensureUnitRunningAfterIdentityChange(unitName, identityChanged)
	case "stopped":
		if err := systemctl("stop", unitName); err != nil {
			return fmt.Errorf("stop %s: %w", unitName, err)
		}
	case "":
		if identityChanged && !plan.GetBool("masked") {
			_, err := restartActiveUnit(unitName)
			return err
		}
	}
	return nil
}

func reconcileUpdateServiceState(unitName string, prior, plan pluginsdk.StateData, identityChanged bool) error {
	if plan.GetString("state") != prior.GetString("state") {
		switch plan.GetString("state") {
		case "running":
			return ensureUnitRunningAfterIdentityChange(unitName, identityChanged)
		case "stopped":
			if err := systemctl("stop", unitName); err != nil {
				return fmt.Errorf("stop %s: %w", unitName, err)
			}
		}
		return nil
	}
	if identityChanged && !plan.GetBool("masked") && plan.GetString("state") != "stopped" {
		_, err := restartActiveUnit(unitName)
		return err
	}
	return nil
}

func ensureUnitRunningAfterIdentityChange(unitName string, identityChanged bool) error {
	if identityChanged {
		restarted, err := restartActiveUnit(unitName)
		if err != nil {
			return err
		}
		if restarted {
			return nil
		}
	}
	if err := systemctl("start", unitName); err != nil {
		return fmt.Errorf("start %s: %w", unitName, err)
	}
	return nil
}

func restartActiveUnit(unitName string) (bool, error) {
	status, err := systemctlShow(unitName)
	if err != nil {
		return false, err
	}
	if desiredStateFromActiveState(status["ActiveState"]) != "running" {
		return false, nil
	}
	if err := systemctl("restart", unitName); err != nil {
		return false, fmt.Errorf("restart %s: %w", unitName, err)
	}
	return true, nil
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

func applyTimesync(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	if err := ensureTimesyncdAvailable(); err != nil {
		return nil, err
	}
	spec, err := desiredTimesyncSpec(plan)
	if err != nil {
		return nil, err
	}
	configChanged, err := writeTimesyncdConfig(spec)
	if err != nil {
		return nil, err
	}
	if spec.Enabled {
		if err := systemctl("enable", timesyncdServiceName); err != nil {
			return nil, fmt.Errorf("enable %s: %w", timesyncdServiceName, err)
		}
		if err := systemctl("restart", timesyncdServiceName); err != nil {
			if !configChanged {
				return nil, fmt.Errorf("restart %s: %w", timesyncdServiceName, err)
			}
			return nil, fmt.Errorf("restart %s after config update: %w", timesyncdServiceName, err)
		}
	} else {
		if err := systemctl("stop", timesyncdServiceName); err != nil {
			return nil, fmt.Errorf("stop %s: %w", timesyncdServiceName, err)
		}
		if err := systemctl("disable", timesyncdServiceName); err != nil {
			return nil, fmt.Errorf("disable %s: %w", timesyncdServiceName, err)
		}
	}
	return (&timesyncResource{}).Read(nil)
}

func desiredTimesyncSpec(config pluginsdk.StateData) (*timesyncSpec, error) {
	servers, err := normalizeTimesyncServers(config.GetStringList("servers"), "servers")
	if err != nil {
		return nil, err
	}
	fallbackServers, err := normalizeTimesyncServers(config.GetStringList("fallback_servers"), "fallback_servers")
	if err != nil {
		return nil, err
	}
	enabled := true
	if _, ok := config["enabled"]; ok {
		enabled = config.GetBool("enabled")
	}
	return &timesyncSpec{
		Enabled:         enabled,
		Servers:         servers,
		FallbackServers: fallbackServers,
	}, nil
}

func normalizeTimesyncServers(values []string, fieldName string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil, fmt.Errorf("%s must not contain empty values", fieldName)
		}
		if strings.ContainsAny(trimmed, " \t\r\n") {
			return nil, fmt.Errorf("%s entry %q must not contain whitespace", fieldName, value)
		}
		if strings.Contains(trimmed, "#") {
			return nil, fmt.Errorf("%s entry %q must not contain comment markers", fieldName, value)
		}
		result = append(result, trimmed)
	}
	return result, nil
}

func timesyncdEnabled(status map[string]string) bool {
	return isEnabledUnitState(status["UnitFileState"]) && desiredStateFromActiveState(status["ActiveState"]) == "running"
}

func readTimesyncdConfig() ([]string, []string, error) {
	exists, err := pluginsdk.FileExists(timesyncdConfigPath)
	if err != nil {
		return nil, nil, fmt.Errorf("check %s: %w", timesyncdConfigPath, err)
	}
	if !exists {
		return []string{}, []string{}, nil
	}
	data, err := pluginsdk.FileRead(timesyncdConfigPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", timesyncdConfigPath, err)
	}
	servers, fallbackServers := parseTimesyncdConfig(string(data))
	return servers, fallbackServers, nil
}

func parseTimesyncdConfig(content string) ([]string, []string) {
	var servers []string
	var fallbackServers []string
	inTimeSection := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inTimeSection = trimmed == "[Time]"
			continue
		}
		if !inTimeSection {
			continue
		}
		key, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "NTP":
			servers = append([]string{}, strings.Fields(strings.TrimSpace(value))...)
		case "FallbackNTP":
			fallbackServers = append([]string{}, strings.Fields(strings.TrimSpace(value))...)
		}
	}
	if servers == nil {
		servers = []string{}
	}
	if fallbackServers == nil {
		fallbackServers = []string{}
	}
	return servers, fallbackServers
}

func renderTimesyncdConfig(spec *timesyncSpec) string {
	lines := []string{
		"# Managed by tf-linux-provider. Changes will be overwritten.",
		"[Time]",
	}
	if len(spec.Servers) > 0 {
		lines = append(lines, "NTP="+strings.Join(spec.Servers, " "))
	}
	if len(spec.FallbackServers) > 0 {
		lines = append(lines, "FallbackNTP="+strings.Join(spec.FallbackServers, " "))
	}
	return strings.Join(lines, "\n") + "\n"
}

func writeTimesyncdConfig(spec *timesyncSpec) (bool, error) {
	result, err := pluginsdk.CmdExec("mkdir", []string{"-p", timesyncdConfigDir})
	if err != nil {
		return false, fmt.Errorf("ensure %s: %w", timesyncdConfigDir, err)
	}
	if result.ExitCode != 0 {
		return false, fmt.Errorf("ensure %s failed (%s)", timesyncdConfigDir, pluginsdk.CommandFailureDetail(result))
	}
	rendered := renderTimesyncdConfig(spec)
	existing, err := pluginsdk.FileRead(timesyncdConfigPath)
	if err == nil && string(existing) == rendered {
		return false, nil
	}
	if err := pluginsdk.FileWrite(timesyncdConfigPath, []byte(rendered), 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", timesyncdConfigPath, err)
	}
	return true, nil
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

func ensureSystemctlOperational(feature string) error {
	res, err := pluginsdk.CmdExec("systemctl", []string{"show", "--property=Version", "--value"})
	if err != nil {
		return fmt.Errorf("%s requires a working systemd manager: systemctl show Version: %w", feature, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s requires a working systemd manager: systemctl show Version failed (%s)", feature, pluginsdk.CommandFailureDetail(res))
	}
	return nil
}

func ensureTimesyncdAvailable() error {
	if err := ensureCommandAvailable("systemctl", "timesync"); err != nil {
		return err
	}
	if err := ensureSystemctlOperational("timesync"); err != nil {
		return err
	}
	if err := ensureSystemdUnitExists(timesyncdServiceName); err != nil {
		return fmt.Errorf("timesync requires %s: %w", timesyncdServiceName, err)
	}
	return nil
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
	tmpPath, err := hostfs.WriteTempFile("tf-linux-provider-systemd-validate-"+sanitizeValidationName(baseName), ext, []byte(content), 0o644)
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
	pluginsdk.RegisterResource(&timesyncResource{})
	pluginsdk.RegisterDataSource(&systemdUnitInfoDataSource{})
	pluginsdk.RegisterAction(&restartProcessAction{})
}

func main() {}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
