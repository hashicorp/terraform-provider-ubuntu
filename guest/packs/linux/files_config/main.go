package main

import (
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
	linuxfilescontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/linuxfiles"
	linuxnetworkcontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/linuxnetwork"
	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/hostsfile"
)

const sshdConfigPath = "/etc/ssh/sshd_config"

const (
	sysctlConfigPath       = "/etc/sysctl.conf"
	networkStackConfigPath = "/etc/sysctl.d/90-tf-linux-provider-network-stack.conf"
	fstabConfigPath        = "/etc/fstab"
	swapInfoPath           = "/proc/swaps"
)

const swapDisabledCommentPrefix = "# tf-linux-provider swap disabled: "

const (
	dirPort                   = "Port"
	dirPermitRootLogin        = "PermitRootLogin"
	dirPasswordAuthentication = "PasswordAuthentication"
	dirPubkeyAuthentication   = "PubkeyAuthentication"
	dirMaxAuthTries           = "MaxAuthTries"
	dirX11Forwarding          = "X11Forwarding"
	dirAllowUsers             = "AllowUsers"
	dirAllowGroups            = "AllowGroups"
	dirClientAliveInterval    = "ClientAliveInterval"
	dirClientAliveCountMax    = "ClientAliveCountMax"
)

var schemaToDirective = map[string]string{
	"port":                    dirPort,
	"permit_root_login":       dirPermitRootLogin,
	"password_authentication": dirPasswordAuthentication,
	"pubkey_authentication":   dirPubkeyAuthentication,
	"max_auth_tries":          dirMaxAuthTries,
	"x11_forwarding":          dirX11Forwarding,
	"allow_users":             dirAllowUsers,
	"allow_groups":            dirAllowGroups,
	"client_alive_interval":   dirClientAliveInterval,
	"client_alive_count_max":  dirClientAliveCountMax,
}

var directiveToSchema = func() map[string]string {
	result := make(map[string]string, len(schemaToDirective))
	for attr, dir := range schemaToDirective {
		result[dir] = attr
	}
	return result
}()

var managedDirectives = func() map[string]bool {
	result := make(map[string]bool, len(schemaToDirective))
	for _, dir := range schemaToDirective {
		result[dir] = true
	}
	return result
}()

var intDirectives = map[string]bool{
	dirPort:                true,
	dirMaxAuthTries:        true,
	dirClientAliveInterval: true,
	dirClientAliveCountMax: true,
}

var listDirectives = map[string]bool{
	dirAllowUsers:  true,
	dirAllowGroups: true,
}

var validYesNo = map[string]map[string]bool{
	"password_authentication": {"yes": true, "no": true},
	"pubkey_authentication":   {"yes": true, "no": true},
	"x11_forwarding":          {"yes": true, "no": true},
	"permit_root_login":       {"yes": true, "no": true, "prohibit-password": true, "without-password": true, "forced-commands-only": true},
}

type fileResource struct{}

type symlinkResource struct{}

type kernelModulesResource struct{}

type swapResource struct{}

type networkStackResource struct{}

type fileValidationSpec = linuxfilescontract.FileValidation

func (r *fileResource) Name() string { return "file" }

func (r *symlinkResource) Name() string { return "symlink" }

func (r *kernelModulesResource) Name() string { return "kernel_modules" }

func (r *swapResource) Name() string { return "swap" }

func (r *networkStackResource) Name() string { return "network_stack" }

func (r *kernelModulesResource) Schema() pluginsdk.Schema {
	return linuxfilescontract.KernelModulesResourceSchema()
}

func (r *kernelModulesResource) Validate(config pluginsdk.StateData) error {
	path := config.GetString("path")
	if path == "" || !strings.HasPrefix(path, "/") {
		return fmt.Errorf("path must be an absolute path, got %q", path)
	}
	if _, err := normalizeKernelModules(config.GetStringList("modules")); err != nil {
		return err
	}
	return nil
}

func (r *kernelModulesResource) Read(state pluginsdk.StateData) (pluginsdk.StateData, error) {
	path := state.GetString("path")
	stat, err := pluginsdk.FileStat_(path)
	if err != nil {
		if isNotExistError(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if stat.IsDir {
		return nil, fmt.Errorf("path %s is a directory, expected a file", path)
	}

	content, err := pluginsdk.FileRead(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	return buildKernelModulesState(path, parseKernelModulesContent(string(content))), nil
}

func (r *kernelModulesResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applyKernelModules(plan)
}

func (r *kernelModulesResource) Update(_ pluginsdk.StateData, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applyKernelModules(plan)
}

func (r *kernelModulesResource) Delete(state pluginsdk.StateData) error {
	path := state.GetString("path")
	if path == "" {
		return nil
	}

	stat, err := pluginsdk.FileStat_(path)
	if err != nil {
		if isNotExistError(err) {
			return nil
		}
		return fmt.Errorf("stat %s before delete: %w", path, err)
	}
	if stat.IsDir {
		return fmt.Errorf("path %s is a directory, refusing file delete", path)
	}

	if err := pluginsdk.FileDelete(path); err != nil {
		return fmt.Errorf("delete %s: %w", path, err)
	}
	return nil
}

func (r *kernelModulesResource) ImportState(id string) (pluginsdk.StateData, error) {
	if id == "" || !strings.HasPrefix(id, "/") {
		return nil, fmt.Errorf("import ID must be an absolute file path, got %q", id)
	}
	return r.Read(pluginsdk.StateData{"path": id})
}

func (r *swapResource) Schema() pluginsdk.Schema {
	return linuxfilescontract.SwapResourceSchema()
}

func (r *swapResource) Validate(config pluginsdk.StateData) error {
	if _, ok := config["enabled"]; !ok {
		return fmt.Errorf("enabled must be set")
	}
	return nil
}

func (r *swapResource) Read(pluginsdk.StateData) (pluginsdk.StateData, error) {
	enabled, err := readSwapEnabledState()
	if err != nil {
		return nil, err
	}
	return buildSwapState(enabled), nil
}

func (r *swapResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applySwap(plan)
}

func (r *swapResource) Update(_ pluginsdk.StateData, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applySwap(plan)
}

func (r *swapResource) Delete(state pluginsdk.StateData) error {
	if state == nil || state.GetBool("enabled") {
		return nil
	}
	return enableManagedSwap(false)
}

func (r *swapResource) ImportState(id string) (pluginsdk.StateData, error) {
	trimmed := strings.TrimSpace(id)
	if trimmed != "" && trimmed != "system" && trimmed != "swap" {
		return nil, fmt.Errorf("import ID must be empty, \"system\", or \"swap\", got %q", id)
	}
	return r.Read(nil)
}

func (r *fileResource) Schema() pluginsdk.Schema {
	return linuxfilescontract.FileResourceSchema()
}

func (r *symlinkResource) Schema() pluginsdk.Schema {
	return linuxfilescontract.SymlinkResourceSchema()
}

func (r *fileResource) Validate(config pluginsdk.StateData) error {
	path := config.GetString("path")
	if path == "" || !strings.HasPrefix(path, "/") {
		return fmt.Errorf("path must be an absolute path, got %q", path)
	}
	if mode, ok := config["mode"]; ok && strings.TrimSpace(config.GetString("mode")) != "" {
		if _, err := parseOctal(config.GetString("mode")); err != nil {
			return fmt.Errorf("invalid mode %v: %w", mode, err)
		}
	}

	hasContent := config.GetString("content") != ""
	hasBase64 := config.GetString("content_base64") != ""

	if !hasContent && !hasBase64 {
		return fmt.Errorf("exactly one of content or content_base64 must be set")
	}
	if hasContent && hasBase64 {
		return fmt.Errorf("content and content_base64 are mutually exclusive")
	}

	validation, err := resolveFileValidation(config)
	if err != nil {
		return err
	}
	if validation != nil {
		hasCommand, err := pluginsdk.HostHasCommand(validation.Argv[0])
		if err != nil {
			return fmt.Errorf("check validation command %q: %w", validation.Argv[0], err)
		}
		if !hasCommand {
			return fmt.Errorf("validation command %q is not available", validation.Argv[0])
		}
	}

	return nil
}

func (r *symlinkResource) Validate(config pluginsdk.StateData) error {
	path := config.GetString("path")
	if path == "" || !strings.HasPrefix(path, "/") {
		return fmt.Errorf("path must be an absolute path, got %q", path)
	}
	if strings.TrimSpace(config.GetString("target")) == "" {
		return fmt.Errorf("target must not be empty")
	}
	return nil
}

func (r *fileResource) Read(state pluginsdk.StateData) (pluginsdk.StateData, error) {
	path := state.GetString("path")
	stat, err := pluginsdk.FileStat_(path)
	if err != nil {
		if isNotExistError(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if stat.IsDir {
		return nil, fmt.Errorf("path %s is a directory, expected a file", path)
	}

	out := pluginsdk.StateData{
		"id":        path,
		"path":      path,
		"sensitive": fileStateSensitive(state),
		"owner":     stat.Owner,
		"group":     stat.Group,
		"mode":      fmt.Sprintf("%04o", stat.Mode),
		"digest":    stat.Digest,
	}

	if _, ok := state["content"]; ok {
		out["content"] = fileStateContentValue(state, stat.Digest, false)
	}
	if _, ok := state["content_base64"]; ok {
		out["content_base64"] = fileStateContentValue(state, stat.Digest, true)
	}
	if v := state.GetString("run_as"); v != "" {
		out["run_as"] = v
	}
	if validation, err := fileValidationStateValue(state); err != nil {
		return nil, fmt.Errorf("read file validation state: %w", err)
	} else if validation != nil {
		out["validation"] = validation
	}

	return out, nil
}

func (r *symlinkResource) Read(state pluginsdk.StateData) (pluginsdk.StateData, error) {
	path := state.GetString("path")
	linkState, target, err := symlinkTarget(path)
	if err != nil {
		return nil, err
	}
	if linkState == symlinkStateAbsent {
		return nil, nil
	}
	if linkState != symlinkStateLink {
		return nil, fmt.Errorf("path %s exists but is not a symlink", path)
	}

	out := pluginsdk.StateData{
		"id":     path,
		"path":   path,
		"target": target,
	}
	if v := state.GetString("run_as"); v != "" {
		out["run_as"] = v
	}
	return out, nil
}

func (r *fileResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	currentStat, err := pluginsdk.FileStat_(plan.GetString("path"))
	if err != nil && !isNotExistError(err) {
		return nil, fmt.Errorf("stat %s before write: %w", plan.GetString("path"), err)
	}
	if currentStat != nil && currentStat.IsDir {
		return nil, fmt.Errorf("path %s is a directory, expected a file", plan.GetString("path"))
	}
	if currentStat != nil {
		return nil, importRequiredError("file", plan.GetString("path"))
	}

	return applyFile(plan)
}

func (r *symlinkResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	linkState, _, err := symlinkTarget(plan.GetString("path"))
	if err != nil {
		return nil, err
	}
	if linkState == symlinkStateRegular {
		return nil, fmt.Errorf("path %s exists but is not a symlink", plan.GetString("path"))
	}
	if linkState == symlinkStateLink {
		return nil, importRequiredError("symlink", plan.GetString("path"))
	}

	return applySymlink(plan)
}

func (r *fileResource) Update(_ pluginsdk.StateData, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applyFile(plan)
}

func (r *symlinkResource) Update(_ pluginsdk.StateData, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applySymlink(plan)
}

func applySymlink(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	path := plan.GetString("path")
	target := strings.TrimSpace(plan.GetString("target"))
	if err := ensureParentDir(path); err != nil {
		return nil, err
	}
	if err := createManagedSymlink(target, path); err != nil {
		return nil, err
	}
	return buildSymlinkState(plan), nil
}

func applyFile(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	path := plan.GetString("path")
	owner := withDefault(plan.GetString("owner"), "root")
	group := withDefault(plan.GetString("group"), "root")
	mode := withDefault(plan.GetString("mode"), "0644")
	validation, err := resolveFileValidation(plan)
	if err != nil {
		return nil, err
	}

	data, err := resolveContent(plan)
	if err != nil {
		return nil, err
	}

	modeVal, err := parseOctal(mode)
	if err != nil {
		return nil, fmt.Errorf("invalid mode %q: %w", mode, err)
	}
	currentStat, err := pluginsdk.FileStat_(path)
	if err != nil && !isNotExistError(err) {
		return nil, fmt.Errorf("stat %s before write: %w", path, err)
	}
	if currentStat != nil && currentStat.IsDir {
		return nil, fmt.Errorf("path %s is a directory, expected a file", path)
	}
	if err := ensureParentDir(path); err != nil {
		return nil, err
	}

	if validation != nil {
		if validation.InPlace {
			return applyFileWithInPlaceValidation(plan, currentStat, data, owner, group, mode, modeVal, validation)
		}
		return applyFileWithStagedValidation(plan, data, owner, group, mode, modeVal, validation)
	}

	if err := pluginsdk.FileWrite(path, data, modeVal); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}

	if err := pluginsdk.FileChown(path, owner, group); err != nil {
		return nil, fmt.Errorf("chown %s: %w", path, err)
	}

	stat, err := pluginsdk.FileStat_(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s after create: %w", path, err)
	}

	return buildFileState(plan, stat, owner, group, mode), nil
}

func applyFileWithStagedValidation(
	plan pluginsdk.StateData,
	data []byte,
	owner, group, mode string,
	modeVal uint32,
	validation *fileValidationSpec,
) (pluginsdk.StateData, error) {
	path := plan.GetString("path")
	candidatePath := fileValidationTempPath(path, "candidate")

	if err := writeManagedFile(candidatePath, data, modeVal, owner, group); err != nil {
		return nil, err
	}
	cleanupCandidate := true
	defer func() {
		if cleanupCandidate {
			_ = removeManagedFilePath(candidatePath)
		}
	}()

	if err := runFileValidation(validation, candidatePath); err != nil {
		return nil, err
	}
	if err := moveManagedFilePath(candidatePath, path); err != nil {
		return nil, err
	}
	cleanupCandidate = false

	stat, err := pluginsdk.FileStat_(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s after validated write: %w", path, err)
	}

	return buildFileState(plan, stat, owner, group, mode), nil
}

func applyFileWithInPlaceValidation(
	plan pluginsdk.StateData,
	currentStat *pluginsdk.FileStat,
	data []byte,
	owner, group, mode string,
	modeVal uint32,
	validation *fileValidationSpec,
) (pluginsdk.StateData, error) {
	path := plan.GetString("path")
	candidatePath := fileValidationTempPath(path, "candidate")
	backupPath := ""

	if err := writeManagedFile(candidatePath, data, modeVal, owner, group); err != nil {
		return nil, err
	}
	cleanupCandidate := true
	defer func() {
		if cleanupCandidate {
			_ = removeManagedFilePath(candidatePath)
		}
	}()

	if currentStat != nil {
		backupPath = fileValidationTempPath(path, "backup")
		if err := moveManagedFilePath(path, backupPath); err != nil {
			return nil, err
		}
	}

	if err := moveManagedFilePath(candidatePath, path); err != nil {
		if backupPath != "" {
			_ = moveManagedFilePath(backupPath, path)
		}
		return nil, err
	}
	cleanupCandidate = false

	if err := runFileValidation(validation, path); err != nil {
		if rollbackErr := rollbackValidatedFile(path, backupPath); rollbackErr != nil {
			return nil, fmt.Errorf("%w; rollback failed: %v", err, rollbackErr)
		}
		return nil, err
	}

	if backupPath != "" {
		if err := removeManagedFilePath(backupPath); err != nil {
			return nil, err
		}
	}

	stat, err := pluginsdk.FileStat_(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s after in-place validated write: %w", path, err)
	}

	return buildFileState(plan, stat, owner, group, mode), nil
}

func applyKernelModules(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	path := plan.GetString("path")
	modules, err := normalizeKernelModules(plan.GetStringList("modules"))
	if err != nil {
		return nil, err
	}
	if err := ensureParentDir(path); err != nil {
		return nil, err
	}

	if err := pluginsdk.FileWrite(path, []byte(serializeKernelModules(modules)), 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	if err := pluginsdk.FileChown(path, "root", "root"); err != nil {
		return nil, fmt.Errorf("chown %s: %w", path, err)
	}

	for _, module := range modules {
		if err := loadKernelModule(module); err != nil {
			return nil, err
		}
	}

	return buildKernelModulesState(path, modules), nil
}

func applySwap(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	if plan.GetBool("enabled") {
		if err := enableManagedSwap(true); err != nil {
			return nil, err
		}
	} else {
		if err := disableManagedSwap(); err != nil {
			return nil, err
		}
	}
	return buildSwapState(plan.GetBool("enabled")), nil
}

func (r *fileResource) Delete(state pluginsdk.StateData) error {
	path := state.GetString("path")
	stat, err := pluginsdk.FileStat_(path)
	if err != nil {
		if isNotExistError(err) {
			return nil
		}
		return fmt.Errorf("stat %s before delete: %w", path, err)
	}
	if stat.IsDir {
		return fmt.Errorf("path %s is a directory, refusing file delete", path)
	}
	if err := pluginsdk.FileDelete(path); err != nil {
		return fmt.Errorf("delete %s: %w", path, err)
	}
	return nil
}

func (r *symlinkResource) Delete(state pluginsdk.StateData) error {
	path := state.GetString("path")
	linkState, _, err := symlinkTarget(path)
	if err != nil {
		return err
	}
	if linkState == symlinkStateAbsent {
		return nil
	}
	if linkState != symlinkStateLink {
		return fmt.Errorf("path %s exists but is not a symlink", path)
	}
	if err := removeManagedFilePath(path); err != nil {
		return err
	}
	return nil
}

func (r *fileResource) ImportState(id string) (pluginsdk.StateData, error) {
	if id == "" || !strings.HasPrefix(id, "/") {
		return nil, fmt.Errorf("import ID must be an absolute file path, got %q", id)
	}
	return r.Read(pluginsdk.StateData{"path": id})
}

func (r *symlinkResource) ImportState(id string) (pluginsdk.StateData, error) {
	if id == "" || !strings.HasPrefix(id, "/") {
		return nil, fmt.Errorf("import ID must be an absolute symlink path, got %q", id)
	}
	return r.Read(pluginsdk.StateData{"path": id})
}

type fileInfoDataSource struct{}

func (d *fileInfoDataSource) Name() string { return "file_info" }

func (d *fileInfoDataSource) DataSchema() pluginsdk.Schema {
	return linuxfilescontract.FileInfoDataSourceSchema()
}

func (d *fileInfoDataSource) DataRead(config pluginsdk.StateData) (pluginsdk.StateData, error) {
	path := config.GetString("path")
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	runAs := strings.TrimSpace(config.GetString("run_as"))

	stat, err := pluginsdk.FileStat_(path)
	if err != nil {
		if !isNotExistError(err) {
			return nil, fmt.Errorf("stat %s: %w", path, err)
		}
		state := pluginsdk.StateData{
			"id":         path,
			"path":       path,
			"exists":     false,
			"size":       0,
			"mode":       "",
			"owner":      "",
			"group":      "",
			"digest":     "",
			"mtime_unix": 0,
		}
		if runAs != "" {
			state["run_as"] = runAs
		}
		return state, nil
	}

	state := pluginsdk.StateData{
		"id":         path,
		"path":       path,
		"exists":     true,
		"size":       stat.Size,
		"mode":       fmt.Sprintf("%04o", stat.Mode),
		"owner":      stat.Owner,
		"group":      stat.Group,
		"digest":     stat.Digest,
		"mtime_unix": stat.ModTime,
	}
	if runAs != "" {
		state["run_as"] = runAs
	}
	return state, nil
}

type hostsEntryResource struct{}

func (r *hostsEntryResource) Name() string { return "hosts_entry" }

func (r *hostsEntryResource) Schema() pluginsdk.Schema {
	return linuxfilescontract.HostsEntryResourceSchema()
}

func (r *hostsEntryResource) Validate(config pluginsdk.StateData) error {
	ip := config.GetString("ip")
	if net.ParseIP(ip) == nil {
		return fmt.Errorf("invalid IP address: %q", ip)
	}
	hostname := config.GetString("hostname")
	if strings.TrimSpace(hostname) == "" {
		return fmt.Errorf("hostname must not be empty")
	}
	if !isValidHostToken(hostname) {
		return fmt.Errorf("invalid hostname %q", hostname)
	}
	for _, alias := range config.GetStringList("aliases") {
		if !isValidHostToken(alias) {
			return fmt.Errorf("invalid alias %q", alias)
		}
	}
	return nil
}

func (r *hostsEntryResource) Read(state pluginsdk.StateData) (pluginsdk.StateData, error) {
	ip := state.GetString("ip")
	hostname := state.GetString("hostname")

	entry, err := findEntry(ip, hostname)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, nil
	}

	out := pluginsdk.StateData{
		"id":       hostsEntryID(entry.IP, entry.Hostname),
		"ip":       entry.IP,
		"hostname": entry.Hostname,
	}
	if len(entry.Aliases) > 0 {
		out["aliases"] = entry.Aliases
	}
	if comment := hostsCommentState(entry.Comment); comment != "" {
		out["comment"] = comment
	}
	return out, nil
}

func (r *hostsEntryResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	ip := plan.GetString("ip")
	hostname := plan.GetString("hostname")
	aliases := plan.GetStringList("aliases")
	comment := plan.GetString("comment")

	lock, err := pluginsdk.FileLock("/etc/hosts")
	if err != nil {
		return nil, fmt.Errorf("lock /etc/hosts: %w", err)
	}
	defer pluginsdk.FileUnlock(lock)

	content, err := pluginsdk.FileRead("/etc/hosts")
	if err != nil {
		return nil, fmt.Errorf("read /etc/hosts: %w", err)
	}

	entries, err := hostsfile.Parse(content)
	if err != nil {
		return nil, fmt.Errorf("parse /etc/hosts: %w", err)
	}

	desired := pluginsdk.HostEntry{
		IP:       ip,
		Hostname: hostname,
		Aliases:  normalizeHostAliases(hostname, aliases),
		Comment:  serializeHostsComment(comment),
	}
	for _, entry := range entries {
		if entry.IP == desired.IP && entry.Hostname == desired.Hostname {
			return nil, importRequiredError("hosts entry", hostsEntryID(desired.IP, desired.Hostname))
		}
	}
	entries = upsertHostsEntry(entries, desired)

	if err := writeHosts(entries); err != nil {
		return nil, err
	}

	state := pluginsdk.StateData{"id": hostsEntryID(ip, hostname), "ip": ip, "hostname": hostname}
	if len(desired.Aliases) > 0 {
		state["aliases"] = desired.Aliases
	}
	if comment != "" {
		state["comment"] = comment
	}
	return state, nil
}

func (r *hostsEntryResource) Update(prior, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	ip := prior.GetString("ip")
	hostname := prior.GetString("hostname")
	aliases := plan.GetStringList("aliases")
	comment := plan.GetString("comment")

	lock, err := pluginsdk.FileLock("/etc/hosts")
	if err != nil {
		return nil, fmt.Errorf("lock /etc/hosts: %w", err)
	}
	defer pluginsdk.FileUnlock(lock)

	content, err := pluginsdk.FileRead("/etc/hosts")
	if err != nil {
		return nil, fmt.Errorf("read /etc/hosts: %w", err)
	}

	entries, err := hostsfile.Parse(content)
	if err != nil {
		return nil, fmt.Errorf("parse /etc/hosts: %w", err)
	}

	found := false
	normalizedAliases := normalizeHostAliases(hostname, aliases)
	normalizedComment := serializeHostsComment(comment)
	for i, e := range entries {
		if e.IP == ip && e.Hostname == hostname {
			entries[i].Aliases = normalizedAliases
			entries[i].Comment = normalizedComment
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("hosts entry %s %s not found during update", ip, hostname)
	}

	if err := writeHosts(entries); err != nil {
		return nil, err
	}

	state := pluginsdk.StateData{"id": hostsEntryID(ip, hostname), "ip": ip, "hostname": hostname}
	if len(normalizedAliases) > 0 {
		state["aliases"] = normalizedAliases
	}
	if comment != "" {
		state["comment"] = comment
	}
	return state, nil
}

func (r *hostsEntryResource) Delete(state pluginsdk.StateData) error {
	ip := state.GetString("ip")
	hostname := state.GetString("hostname")

	lock, err := pluginsdk.FileLock("/etc/hosts")
	if err != nil {
		return fmt.Errorf("lock /etc/hosts: %w", err)
	}
	defer pluginsdk.FileUnlock(lock)

	content, err := pluginsdk.FileRead("/etc/hosts")
	if err != nil {
		return fmt.Errorf("read /etc/hosts: %w", err)
	}

	entries, err := hostsfile.Parse(content)
	if err != nil {
		return fmt.Errorf("parse /etc/hosts: %w", err)
	}

	filtered := make([]pluginsdk.HostEntry, 0, len(entries))
	for _, e := range entries {
		if e.IP == ip && e.Hostname == hostname {
			continue
		}
		filtered = append(filtered, e)
	}

	return writeHosts(filtered)
}

func (r *hostsEntryResource) ImportState(id string) (pluginsdk.StateData, error) {
	parts := strings.SplitN(id, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("import ID must be in the format \"ip/hostname\", got %q", id)
	}
	return r.Read(pluginsdk.StateData{"ip": parts[0], "hostname": parts[1]})
}

type sysctlEntryResource struct{}

func (r *sysctlEntryResource) Name() string { return "sysctl_entry" }

type networkStackKeySpec struct {
	Attribute string
	Key       string
}

var networkStackKeySpecs = []networkStackKeySpec{
	{Attribute: "ipv4_forwarding", Key: "net.ipv4.ip_forward"},
	{Attribute: "ipv6_forwarding", Key: "net.ipv6.conf.all.forwarding"},
	{Attribute: "ipv6_forwarding", Key: "net.ipv6.conf.default.forwarding"},
	{Attribute: "bridge_netfilter_ipv4", Key: "net.bridge.bridge-nf-call-iptables"},
	{Attribute: "bridge_netfilter_ipv6", Key: "net.bridge.bridge-nf-call-ip6tables"},
}

func (r *networkStackResource) Schema() pluginsdk.Schema {
	return linuxnetworkcontract.NetworkStackResourceSchema()
}

func (r *networkStackResource) Validate(_ pluginsdk.StateData) error {
	return nil
}

func (r *networkStackResource) Read(_ pluginsdk.StateData) (pluginsdk.StateData, error) {
	content, err := pluginsdk.FileRead(networkStackConfigPath)
	if err != nil {
		if isNotExistError(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", networkStackConfigPath, err)
	}

	entries := parseKeyValueLines(string(content), "=")
	values := make(map[string]string, len(networkStackKeySpecs))
	for _, entry := range entries {
		if entry.Key != "" {
			values[entry.Key] = strings.TrimSpace(entry.Value)
		}
	}

	return buildNetworkStackState(values), nil
}

func (r *networkStackResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	stat, err := pluginsdk.FileStat_(networkStackConfigPath)
	if err != nil && !isNotExistError(err) {
		return nil, fmt.Errorf("stat %s: %w", networkStackConfigPath, err)
	}
	if stat != nil {
		if stat.IsDir {
			return nil, fmt.Errorf("path %s is a directory, expected a file", networkStackConfigPath)
		}
		return nil, importRequiredError("network stack", networkStackID())
	}
	return applyNetworkStack(plan)
}

func (r *networkStackResource) Update(_ pluginsdk.StateData, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applyNetworkStack(plan)
}

func (r *networkStackResource) Delete(_ pluginsdk.StateData) error {
	stat, err := pluginsdk.FileStat_(networkStackConfigPath)
	if err != nil {
		if isNotExistError(err) {
			return nil
		}
		return fmt.Errorf("stat %s before delete: %w", networkStackConfigPath, err)
	}
	if stat.IsDir {
		return fmt.Errorf("path %s is a directory, expected a file", networkStackConfigPath)
	}
	if err := pluginsdk.FileDelete(networkStackConfigPath); err != nil {
		return fmt.Errorf("delete %s: %w", networkStackConfigPath, err)
	}
	return nil
}

func (r *networkStackResource) ImportState(id string) (pluginsdk.StateData, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("import ID must be %q or %q", networkStackID(), networkStackConfigPath)
	}
	if id != networkStackID() && id != networkStackConfigPath {
		return nil, fmt.Errorf("import ID must be %q or %q, got %q", networkStackID(), networkStackConfigPath, id)
	}
	return r.Read(nil)
}

func (r *sysctlEntryResource) Schema() pluginsdk.Schema {
	return linuxfilescontract.SysctlEntryResourceSchema()
}

func (r *sysctlEntryResource) Validate(config pluginsdk.StateData) error {
	if strings.TrimSpace(config.GetString("key")) == "" {
		return fmt.Errorf("key must not be empty")
	}
	if _, ok := config["value"]; !ok {
		return fmt.Errorf("value must be set")
	}
	return nil
}

func (r *sysctlEntryResource) Read(state pluginsdk.StateData) (pluginsdk.StateData, error) {
	entry, err := findKeyValueEntry(sysctlConfigPath, state.GetString("key"), "=")
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, nil
	}

	return pluginsdk.StateData{
		"id":    entry.Key,
		"key":   entry.Key,
		"value": entry.Value,
	}, nil
}

func (r *sysctlEntryResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	entry, err := findKeyValueEntry(sysctlConfigPath, plan.GetString("key"), "=")
	if err != nil {
		return nil, err
	}
	if entry != nil {
		return nil, importRequiredError("sysctl entry", plan.GetString("key"))
	}
	return applySysctlEntry(plan)
}

func (r *sysctlEntryResource) Update(_ pluginsdk.StateData, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applySysctlEntry(plan)
}

func applySysctlEntry(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	if err := upsertKeyValueEntry(sysctlConfigPath, plan.GetString("key"), plan.GetString("value"), "="); err != nil {
		return nil, err
	}
	if err := applySysctl(plan.GetString("key"), plan.GetString("value")); err != nil {
		return nil, err
	}
	return (&sysctlEntryResource{}).Read(plan)
}

func (r *sysctlEntryResource) Delete(state pluginsdk.StateData) error {
	return deleteKeyValueEntry(sysctlConfigPath, state.GetString("key"), "=")
}

func (r *sysctlEntryResource) ImportState(id string) (pluginsdk.StateData, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("import ID must be a sysctl key")
	}
	return r.Read(pluginsdk.StateData{"key": id})
}

func applyNetworkStack(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	if err := ensureParentDir(networkStackConfigPath); err != nil {
		return nil, err
	}

	values := networkStackManagedValues(plan)
	entries := make([]keyValueLine, 0, len(networkStackKeySpecs)+1)
	entries = append(entries, keyValueLine{Comment: true, Raw: "# managed by tf-linux-provider network_stack"})
	for _, spec := range networkStackKeySpecs {
		entries = append(entries, keyValueLine{Key: spec.Key, Value: values[spec.Key]})
	}

	if err := writeKeyValueFile(networkStackConfigPath, entries, "="); err != nil {
		return nil, err
	}
	for _, spec := range networkStackKeySpecs {
		if err := applySysctl(spec.Key, values[spec.Key]); err != nil {
			return nil, err
		}
	}

	return buildNetworkStackState(values), nil
}

func networkStackID() string {
	return "network_stack"
}

func networkStackManagedValues(plan pluginsdk.StateData) map[string]string {
	values := make(map[string]string, len(networkStackKeySpecs))
	for _, spec := range networkStackKeySpecs {
		value := "0"
		if plan.GetBool(spec.Attribute) {
			value = "1"
		}
		values[spec.Key] = value
	}
	return values
}

func buildNetworkStackState(values map[string]string) pluginsdk.StateData {
	sysctls := make(map[string]string, len(networkStackKeySpecs))
	for _, spec := range networkStackKeySpecs {
		sysctls[spec.Key] = normalizeNetworkStackValue(values[spec.Key])
	}

	return pluginsdk.StateData{
		"id":                    networkStackID(),
		"ipv4_forwarding":       networkStackValueEnabled(sysctls["net.ipv4.ip_forward"]),
		"ipv6_forwarding":       networkStackValueEnabled(sysctls["net.ipv6.conf.all.forwarding"]) && networkStackValueEnabled(sysctls["net.ipv6.conf.default.forwarding"]),
		"bridge_netfilter_ipv4": networkStackValueEnabled(sysctls["net.bridge.bridge-nf-call-iptables"]),
		"bridge_netfilter_ipv6": networkStackValueEnabled(sysctls["net.bridge.bridge-nf-call-ip6tables"]),
		"config_path":           networkStackConfigPath,
		"sysctls":               sysctls,
	}
}

func normalizeNetworkStackValue(value string) string {
	if networkStackValueEnabled(value) {
		return "1"
	}
	return "0"
}

func networkStackValueEnabled(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

type fstabEntryResource struct{}

func (r *fstabEntryResource) Name() string { return "fstab_entry" }

func (r *fstabEntryResource) Schema() pluginsdk.Schema {
	return linuxfilescontract.FstabEntryResourceSchema()
}

func (r *fstabEntryResource) Validate(config pluginsdk.StateData) error {
	for _, key := range []string{"device", "mount", "fstype"} {
		if strings.TrimSpace(config.GetString(key)) == "" {
			return fmt.Errorf("%s must not be empty", key)
		}
	}
	if !strings.HasPrefix(config.GetString("mount"), "/") {
		return fmt.Errorf("mount must be an absolute path")
	}
	if dump := config.GetInt("dump"); dump < 0 {
		return fmt.Errorf("dump must be >= 0")
	}
	if passno := config.GetInt("passno"); passno < 0 {
		return fmt.Errorf("passno must be >= 0")
	}
	return nil
}

func (r *fstabEntryResource) Read(state pluginsdk.StateData) (pluginsdk.StateData, error) {
	entry, err := findFstabEntry(state.GetString("mount"))
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, nil
	}

	result := pluginsdk.StateData{
		"id":     entry.Mount,
		"device": entry.Device,
		"mount":  entry.Mount,
		"fstype": entry.FSType,
		"dump":   entry.Dump,
		"passno": entry.PassNo,
	}
	if len(entry.Options) > 0 {
		result["options"] = entry.Options
	}
	return result, nil
}

func (r *fstabEntryResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	existing, err := findFstabEntry(plan.GetString("mount"))
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, importRequiredError("fstab entry", existing.Mount)
	}
	return applyFstabEntry(plan)
}

func (r *fstabEntryResource) Update(_ pluginsdk.StateData, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applyFstabEntry(plan)
}

func applyFstabEntry(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	entry := fstabEntry{
		Device:  plan.GetString("device"),
		Mount:   plan.GetString("mount"),
		FSType:  plan.GetString("fstype"),
		Options: withDefaultList(plan.GetStringList("options"), []string{"defaults"}),
		Dump:    plan.GetInt("dump"),
		PassNo:  plan.GetInt("passno"),
	}
	if err := upsertFstabEntry(entry); err != nil {
		return nil, err
	}
	return (&fstabEntryResource{}).Read(pluginsdk.StateData{"mount": entry.Mount})
}

func (r *fstabEntryResource) Delete(state pluginsdk.StateData) error {
	return deleteFstabEntry(state.GetString("mount"))
}

func (r *fstabEntryResource) ImportState(id string) (pluginsdk.StateData, error) {
	if strings.TrimSpace(id) == "" || !strings.HasPrefix(id, "/") {
		return nil, fmt.Errorf("import ID must be an absolute mount path")
	}
	return r.Read(pluginsdk.StateData{"mount": id})
}

type sshdConfigResource struct{}

func (r *sshdConfigResource) Name() string { return "sshd_config" }

func (r *sshdConfigResource) Schema() pluginsdk.Schema {
	return linuxfilescontract.SSHDConfigResourceSchema()
}

func (r *sshdConfigResource) Validate(config pluginsdk.StateData) error {
	for attr, allowed := range validYesNo {
		v := config.GetString(attr)
		if v == "" {
			continue
		}
		if !allowed[v] {
			keys := make([]string, 0, len(allowed))
			for key := range allowed {
				keys = append(keys, key)
			}
			return fmt.Errorf("%s must be one of [%s], got %q", attr, strings.Join(keys, ", "), v)
		}
	}
	if port := config.GetInt("port"); port != 0 && (port < 1 || port > 65535) {
		return fmt.Errorf("port must be between 1 and 65535, got %d", port)
	}
	if mat := config.GetInt("max_auth_tries"); mat != 0 && mat < 1 {
		return fmt.Errorf("max_auth_tries must be >= 1, got %d", mat)
	}
	if cai := config.GetInt("client_alive_interval"); cai < 0 {
		return fmt.Errorf("client_alive_interval must be >= 0, got %d", cai)
	}
	if cacm := config.GetInt("client_alive_count_max"); cacm < 0 {
		return fmt.Errorf("client_alive_count_max must be >= 0, got %d", cacm)
	}
	return nil
}

func (r *sshdConfigResource) Read(pluginsdk.StateData) (pluginsdk.StateData, error) {
	content, err := pluginsdk.FileRead(sshdConfigPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", sshdConfigPath, err)
	}
	return directivesToState(parseSSHDConfig(string(content))), nil
}

func (r *sshdConfigResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return r.applyConfig(plan)
}

func (r *sshdConfigResource) Update(_ pluginsdk.StateData, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return r.applyConfig(plan)
}

func (r *sshdConfigResource) Delete(_ pluginsdk.StateData) error {
	lock, err := pluginsdk.FileLock(sshdConfigPath)
	if err != nil {
		return fmt.Errorf("lock %s: %w", sshdConfigPath, err)
	}
	defer pluginsdk.FileUnlock(lock)

	content, err := pluginsdk.FileRead(sshdConfigPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", sshdConfigPath, err)
	}

	lines := strings.Split(string(content), "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		dir := directiveFromLine(line)
		if dir != "" && managedDirectives[dir] {
			continue
		}
		filtered = append(filtered, line)
	}

	newContent := strings.Join(filtered, "\n")
	if newContent == string(content) {
		return nil
	}
	if err := validateSSHDConfig(newContent); err != nil {
		return err
	}
	if err := pluginsdk.FileWrite(sshdConfigPath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("write %s: %w", sshdConfigPath, err)
	}
	return reloadSSHD()
}

func (r *sshdConfigResource) ImportState(id string) (pluginsdk.StateData, error) {
	if id != "sshd_config" {
		return nil, fmt.Errorf("import ID must be \"sshd_config\", got %q", id)
	}
	return r.Read(nil)
}

func (r *sshdConfigResource) applyConfig(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	lock, err := pluginsdk.FileLock(sshdConfigPath)
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", sshdConfigPath, err)
	}
	defer pluginsdk.FileUnlock(lock)

	content, err := pluginsdk.FileRead(sshdConfigPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", sshdConfigPath, err)
	}

	desired := stateToDirectives(plan)
	lines := strings.Split(string(content), "\n")
	written := make(map[string]bool)
	result := make([]string, 0, len(lines)+len(desired))

	for _, line := range lines {
		dir := directiveFromLine(line)
		if dir != "" && managedDirectives[dir] {
			if val, ok := desired[dir]; ok && !written[dir] {
				result = append(result, dir+" "+val)
				written[dir] = true
			}
			continue
		}
		result = append(result, line)
	}
	for dir, val := range desired {
		if !written[dir] {
			result = append(result, dir+" "+val)
		}
	}

	newContent := strings.Join(result, "\n")
	if newContent == string(content) {
		return r.Read(nil)
	}
	if err := validateSSHDConfig(newContent); err != nil {
		return nil, err
	}
	if err := pluginsdk.FileWrite(sshdConfigPath, []byte(newContent), 0644); err != nil {
		return nil, fmt.Errorf("write %s: %w", sshdConfigPath, err)
	}
	if err := reloadSSHD(); err != nil {
		return nil, err
	}
	return r.Read(nil)
}

func parseSSHDConfig(content string) map[string]string {
	directives := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			parts = strings.SplitN(line, "\t", 2)
			if len(parts) != 2 {
				continue
			}
		}
		directives[parts[0]] = strings.TrimSpace(parts[1])
	}
	return directives
}

func directiveFromLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return ""
	}
	for i, ch := range trimmed {
		if ch == ' ' || ch == '\t' {
			return trimmed[:i]
		}
	}
	return trimmed
}

func directivesToState(directives map[string]string) pluginsdk.StateData {
	state := pluginsdk.StateData{}
	state["id"] = "sshd_config"
	for dir, val := range directives {
		attr, ok := directiveToSchema[dir]
		if !ok {
			continue
		}
		if intDirectives[dir] {
			n, err := strconv.Atoi(val)
			if err == nil {
				state[attr] = n
			}
		} else if listDirectives[dir] {
			items := strings.Fields(val)
			if len(items) > 0 {
				state[attr] = items
			}
		} else {
			state[attr] = val
		}
	}
	return state
}

func stateToDirectives(plan pluginsdk.StateData) map[string]string {
	directives := make(map[string]string)
	for attr, dir := range schemaToDirective {
		if _, exists := plan[attr]; !exists {
			continue
		}
		if intDirectives[dir] {
			n := plan.GetInt(attr)
			if n != 0 {
				directives[dir] = strconv.Itoa(n)
			}
		} else if listDirectives[dir] {
			items := plan.GetStringList(attr)
			if len(items) > 0 {
				directives[dir] = strings.Join(items, " ")
			}
		} else {
			v := plan.GetString(attr)
			if v != "" {
				directives[dir] = v
			}
		}
	}
	return directives
}

func validateSSHDConfig(content string) error {
	tmpPath := sshdConfigPath + ".tf-validate"
	if err := pluginsdk.FileWrite(tmpPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write validation file: %w", err)
	}
	defer func() {
		_ = pluginsdk.FileDelete(tmpPath)
	}()
	result, err := pluginsdk.CmdExec("sshd", []string{"-t", "-f", tmpPath})
	if err != nil {
		return fmt.Errorf("run sshd -t: %w", err)
	}
	if result.ExitCode != 0 {
		msg := result.Stderr
		if msg == "" {
			msg = result.Stdout
		}
		return fmt.Errorf("sshd_config validation failed: %s", msg)
	}
	return nil
}

func reloadSSHD() error {
	type reloadAttempt struct {
		cmd  string
		args []string
	}
	attempts := []reloadAttempt{
		{cmd: "systemctl", args: []string{"reload", "sshd"}},
		{cmd: "systemctl", args: []string{"reload", "ssh"}},
		{cmd: "service", args: []string{"sshd", "reload"}},
		{cmd: "service", args: []string{"ssh", "reload"}},
	}
	var failures []string
	for _, attempt := range attempts {
		hasCmd, err := pluginsdk.HostHasCommand(attempt.cmd)
		if err != nil || !hasCmd {
			continue
		}
		result, execErr := pluginsdk.CmdExec(attempt.cmd, attempt.args)
		if execErr == nil && result.ExitCode == 0 {
			return nil
		}
		msg := ""
		if execErr != nil {
			msg = execErr.Error()
		} else {
			msg = strings.TrimSpace(result.Stderr)
			if msg == "" {
				msg = strings.TrimSpace(result.Stdout)
			}
		}
		failures = append(failures, fmt.Sprintf("%s %s: %s", attempt.cmd, strings.Join(attempt.args, " "), msg))
	}
	if len(failures) == 0 {
		return fmt.Errorf("no supported ssh reload command found")
	}
	return fmt.Errorf("failed to reload ssh service: %s", strings.Join(failures, "; "))
}

func findEntry(ip, hostname string) (*pluginsdk.HostEntry, error) {
	content, err := pluginsdk.FileRead("/etc/hosts")
	if err != nil {
		return nil, fmt.Errorf("read /etc/hosts: %w", err)
	}
	entries, err := hostsfile.Parse(content)
	if err != nil {
		return nil, fmt.Errorf("parse /etc/hosts: %w", err)
	}
	for _, e := range entries {
		if e.IP == ip && e.Hostname == hostname {
			return &e, nil
		}
	}
	return nil, nil
}

func writeHosts(entries []pluginsdk.HostEntry) error {
	data, err := hostsfile.Serialize(entries)
	if err != nil {
		return fmt.Errorf("serialize /etc/hosts: %w", err)
	}
	if err := pluginsdk.FileWrite("/etc/hosts", data, 0644); err != nil {
		return fmt.Errorf("write /etc/hosts: %w", err)
	}
	return nil
}

func appendComment(data []byte, comment string) []byte {
	s := strings.TrimRight(string(data), "\n")
	lines := strings.Split(s, "\n")
	if len(lines) > 0 {
		lines[len(lines)-1] = lines[len(lines)-1] + " # " + comment
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

type fstabEntry struct {
	Device  string
	Mount   string
	FSType  string
	Options []string
	Dump    int
	PassNo  int
	Raw     string
	Comment bool
	Blank   bool
}

type keyValueLine struct {
	Key     string
	Value   string
	Raw     string
	Comment bool
	Blank   bool
}

func findKeyValueEntry(path, key, separator string) (*keyValueLine, error) {
	content, err := pluginsdk.FileRead(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	entries := parseKeyValueLines(string(content), separator)
	for _, entry := range entries {
		if entry.Key == key {
			copy := entry
			return &copy, nil
		}
	}
	return nil, nil
}

func upsertKeyValueEntry(path, key, value, separator string) error {
	lock, err := pluginsdk.FileLock(path)
	if err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	defer pluginsdk.FileUnlock(lock)

	content, err := pluginsdk.FileRead(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	entries := parseKeyValueLines(string(content), separator)

	updated := false
	for index := range entries {
		if entries[index].Key == key {
			entries[index].Value = value
			updated = true
		}
	}
	if !updated {
		entries = append(entries, keyValueLine{Key: key, Value: value})
	}

	return writeKeyValueFile(path, entries, separator)
}

func deleteKeyValueEntry(path, key, separator string) error {
	lock, err := pluginsdk.FileLock(path)
	if err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	defer pluginsdk.FileUnlock(lock)

	content, err := pluginsdk.FileRead(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	entries := parseKeyValueLines(string(content), separator)

	filtered := make([]keyValueLine, 0, len(entries))
	for _, entry := range entries {
		if entry.Key == key {
			continue
		}
		filtered = append(filtered, entry)
	}

	return writeKeyValueFile(path, filtered, separator)
}

func writeKeyValueFile(path string, entries []keyValueLine, separator string) error {
	data := []byte(serializeKeyValueLines(entries, separator))
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	if err := pluginsdk.FileWrite(path, data, 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func parseKeyValueLines(content, separator string) []keyValueLine {
	entries := make([]keyValueLine, 0)
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			entries = append(entries, keyValueLine{Blank: true, Raw: line})
			continue
		}
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			entries = append(entries, keyValueLine{Comment: true, Raw: line})
			continue
		}

		idx := strings.Index(trimmed, separator)
		if idx < 0 {
			entries = append(entries, keyValueLine{Comment: true, Raw: line})
			continue
		}

		entries = append(entries, keyValueLine{
			Key:   strings.TrimSpace(trimmed[:idx]),
			Value: strings.TrimSpace(trimmed[idx+len(separator):]),
		})
	}
	return entries
}

func serializeKeyValueLines(entries []keyValueLine, separator string) string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		switch {
		case entry.Blank:
			lines = append(lines, entry.Raw)
		case entry.Comment:
			lines = append(lines, entry.Raw)
		default:
			lines = append(lines, entry.Key+separator+entry.Value)
		}
	}
	return strings.Join(lines, "\n")
}

func importRequiredError(kind, target string) error {
	return fmt.Errorf("%s %q already exists; import it before managing with terraform", kind, target)
}

func applySysctl(key, value string) error {
	result, err := pluginsdk.CmdExec("sysctl", []string{"-w", key + "=" + value})
	if err != nil {
		return fmt.Errorf("apply sysctl %s: %w", key, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("sysctl -w %s failed (exit %d): %s", key, result.ExitCode, result.Stderr)
	}
	return nil
}

func findFstabEntry(mount string) (*fstabEntry, error) {
	entries, err := readFstabEntries()
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Mount == mount {
			copy := entry
			return &copy, nil
		}
	}
	return nil, nil
}

func upsertFstabEntry(entry fstabEntry) error {
	lock, err := pluginsdk.FileLock(fstabConfigPath)
	if err != nil {
		return fmt.Errorf("lock %s: %w", fstabConfigPath, err)
	}
	defer pluginsdk.FileUnlock(lock)

	entries, err := readFstabEntries()
	if err != nil {
		return err
	}

	updated := false
	for index := range entries {
		if entries[index].Mount == entry.Mount {
			entries[index] = entry
			updated = true
		}
	}
	if !updated {
		entries = append(entries, entry)
	}

	return writeFstabEntries(entries)
}

func deleteFstabEntry(mount string) error {
	lock, err := pluginsdk.FileLock(fstabConfigPath)
	if err != nil {
		return fmt.Errorf("lock %s: %w", fstabConfigPath, err)
	}
	defer pluginsdk.FileUnlock(lock)

	entries, err := readFstabEntries()
	if err != nil {
		return err
	}

	filtered := make([]fstabEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Mount == mount {
			continue
		}
		filtered = append(filtered, entry)
	}

	return writeFstabEntries(filtered)
}

func readSwapEnabledState() (bool, error) {
	active, err := activeSwapPresent()
	if err != nil {
		return false, err
	}
	entries, err := readFstabEntries()
	if err != nil {
		return false, err
	}
	return active || hasEnabledSwapEntries(entries), nil
}

func disableManagedSwap() error {
	active, err := activeSwapPresent()
	if err != nil {
		return err
	}

	lock, err := pluginsdk.FileLock(fstabConfigPath)
	if err != nil {
		return fmt.Errorf("lock %s: %w", fstabConfigPath, err)
	}
	defer pluginsdk.FileUnlock(lock)

	entries, err := readFstabEntries()
	if err != nil {
		return err
	}
	updated, changed := disableSwapEntries(entries)
	if changed {
		if err := writeFstabEntries(updated); err != nil {
			return err
		}
	}
	if active {
		return runSwapCommand("swapoff", []string{"-a"})
	}
	return nil
}

func enableManagedSwap(strict bool) error {
	activeBefore, err := activeSwapPresent()
	if err != nil {
		return err
	}

	lock, err := pluginsdk.FileLock(fstabConfigPath)
	if err != nil {
		return fmt.Errorf("lock %s: %w", fstabConfigPath, err)
	}
	defer pluginsdk.FileUnlock(lock)

	entries, err := readFstabEntries()
	if err != nil {
		return err
	}
	updated, changed, restoredAny, err := restoreManagedSwapEntries(entries)
	if err != nil {
		return err
	}
	persistentEnabled := hasEnabledSwapEntries(updated)
	if changed {
		if err := writeFstabEntries(updated); err != nil {
			return err
		}
	}
	if !activeBefore && !persistentEnabled && !restoredAny {
		if strict {
			return fmt.Errorf("cannot enable swap: no active or managed swap entries were found")
		}
		return nil
	}
	if changed || (!activeBefore && persistentEnabled) {
		return runSwapCommand("swapon", []string{"-a"})
	}
	return nil
}

func activeSwapPresent() (bool, error) {
	content, err := pluginsdk.FileRead(swapInfoPath)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", swapInfoPath, err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) <= 1 {
		return false, nil
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) != "" {
			return true, nil
		}
	}
	return false, nil
}

func disableSwapEntries(entries []fstabEntry) ([]fstabEntry, bool) {
	updated := append([]fstabEntry(nil), entries...)
	changed := false
	for index, entry := range entries {
		if entry.Blank || entry.Comment || !strings.EqualFold(strings.TrimSpace(entry.FSType), "swap") {
			continue
		}
		updated[index] = fstabEntry{Comment: true, Raw: swapDisabledCommentPrefix + serializeFstabDataEntry(entry)}
		changed = true
	}
	return updated, changed
}

func restoreManagedSwapEntries(entries []fstabEntry) ([]fstabEntry, bool, bool, error) {
	updated := make([]fstabEntry, 0, len(entries))
	changed := false
	restoredAny := false
	for _, entry := range entries {
		restored, ok, err := parseManagedDisabledSwapEntry(entry)
		if err != nil {
			return nil, false, false, err
		}
		if ok {
			updated = append(updated, restored)
			changed = true
			restoredAny = true
			continue
		}
		updated = append(updated, entry)
	}
	return updated, changed, restoredAny, nil
}

func parseManagedDisabledSwapEntry(entry fstabEntry) (fstabEntry, bool, error) {
	if !entry.Comment {
		return fstabEntry{}, false, nil
	}
	trimmed := strings.TrimSpace(entry.Raw)
	if !strings.HasPrefix(trimmed, swapDisabledCommentPrefix) {
		return fstabEntry{}, false, nil
	}
	restored, err := parseFstabDataLine(strings.TrimSpace(strings.TrimPrefix(trimmed, swapDisabledCommentPrefix)))
	if err != nil {
		return fstabEntry{}, false, fmt.Errorf("parse managed disabled swap entry: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(restored.FSType), "swap") {
		return fstabEntry{}, false, fmt.Errorf("managed disabled swap entry is not a swap line: %q", entry.Raw)
	}
	return restored, true, nil
}

func hasEnabledSwapEntries(entries []fstabEntry) bool {
	for _, entry := range entries {
		if entry.Blank || entry.Comment {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(entry.FSType), "swap") {
			return true
		}
	}
	return false
}

func parseFstabDataLine(line string) (fstabEntry, error) {
	fields := strings.Fields(line)
	if len(fields) < 6 {
		return fstabEntry{}, fmt.Errorf("invalid fstab entry %q", line)
	}
	dump, err := strconv.Atoi(fields[4])
	if err != nil {
		return fstabEntry{}, fmt.Errorf("parse dump value in %q: %w", line, err)
	}
	passNo, err := strconv.Atoi(fields[5])
	if err != nil {
		return fstabEntry{}, fmt.Errorf("parse passno value in %q: %w", line, err)
	}
	return fstabEntry{
		Device:  fields[0],
		Mount:   fields[1],
		FSType:  fields[2],
		Options: strings.Split(fields[3], ","),
		Dump:    dump,
		PassNo:  passNo,
	}, nil
}

func serializeFstabDataEntry(entry fstabEntry) string {
	return fmt.Sprintf("%s\t%s\t%s\t%s\t%d\t%d", entry.Device, entry.Mount, entry.FSType, strings.Join(entry.Options, ","), entry.Dump, entry.PassNo)
}

func runSwapCommand(cmd string, args []string) error {
	result, err := pluginsdk.CmdExec(cmd, args)
	if err != nil {
		return fmt.Errorf("run %s %s: %w", cmd, strings.Join(args, " "), err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("run %s %s failed (%s)", cmd, strings.Join(args, " "), commandFailureDetail(result))
	}
	return nil
}

func readFstabEntries() ([]fstabEntry, error) {
	content, err := pluginsdk.FileRead(fstabConfigPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", fstabConfigPath, err)
	}

	entries := make([]fstabEntry, 0)
	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			entries = append(entries, fstabEntry{Blank: true, Raw: line})
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			entries = append(entries, fstabEntry{Comment: true, Raw: line})
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 6 {
			entries = append(entries, fstabEntry{Comment: true, Raw: line})
			continue
		}

		dump, err := strconv.Atoi(fields[4])
		if err != nil {
			return nil, fmt.Errorf("parse dump value in %s: %w", fstabConfigPath, err)
		}
		passNo, err := strconv.Atoi(fields[5])
		if err != nil {
			return nil, fmt.Errorf("parse passno value in %s: %w", fstabConfigPath, err)
		}

		entries = append(entries, fstabEntry{
			Device:  fields[0],
			Mount:   fields[1],
			FSType:  fields[2],
			Options: strings.Split(fields[3], ","),
			Dump:    dump,
			PassNo:  passNo,
		})
	}

	return entries, nil
}

func writeFstabEntries(entries []fstabEntry) error {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		switch {
		case entry.Blank:
			lines = append(lines, entry.Raw)
		case entry.Comment:
			lines = append(lines, entry.Raw)
		default:
			lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t%s\t%d\t%d", entry.Device, entry.Mount, entry.FSType, strings.Join(entry.Options, ","), entry.Dump, entry.PassNo))
		}
	}
	data := []byte(strings.Join(lines, "\n"))
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	if err := pluginsdk.FileWrite(fstabConfigPath, data, 0644); err != nil {
		return fmt.Errorf("write %s: %w", fstabConfigPath, err)
	}
	return nil
}

func resolveContent(plan pluginsdk.StateData) ([]byte, error) {
	if raw := plan.GetString("content"); raw != "" {
		return []byte(raw), nil
	}
	if b64 := plan.GetString("content_base64"); b64 != "" {
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("decode content_base64: %w", err)
		}
		return data, nil
	}
	return nil, fmt.Errorf("no content provided")
}

func resolveFileValidation(values pluginsdk.StateData) (*fileValidationSpec, error) {
	raw, ok := values["validation"]
	if !ok || raw == nil {
		return nil, nil
	}

	return linuxfilescontract.ParseFileValidation(raw)
}

func fileValidationStateValue(values pluginsdk.StateData) (map[string]interface{}, error) {
	validation, err := resolveFileValidation(values)
	if err != nil || validation == nil {
		return nil, err
	}

	return validation.StateValue(), nil
}

const (
	symlinkStateAbsent  = "absent"
	symlinkStateLink    = "symlink"
	symlinkStateRegular = "regular"
)

func buildSymlinkState(plan pluginsdk.StateData) pluginsdk.StateData {
	state := pluginsdk.StateData{
		"id":     plan.GetString("path"),
		"path":   plan.GetString("path"),
		"target": strings.TrimSpace(plan.GetString("target")),
	}
	if v := plan.GetString("run_as"); v != "" {
		state["run_as"] = v
	}
	return state
}

func createManagedSymlink(target, path string) error {
	if err := pluginsdk.FileSymlink(target, path); err != nil {
		return fmt.Errorf("link %s -> %s: %w", path, target, err)
	}
	return nil
}

func symlinkTarget(path string) (string, string, error) {
	readlink, err := pluginsdk.FileReadlink(path)
	if err != nil {
		exists, existsErr := pluginsdk.FileExists(path)
		if existsErr != nil {
			return "", "", fmt.Errorf("check symlink path %s: %w", path, existsErr)
		}
		if exists {
			return symlinkStateRegular, "", nil
		}
		return symlinkStateAbsent, "", nil
	}
	return symlinkStateLink, strings.TrimSpace(readlink), nil
}

func writeManagedFile(path string, data []byte, mode uint32, owner, group string) error {
	if err := pluginsdk.FileWrite(path, data, mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := pluginsdk.FileChown(path, owner, group); err != nil {
		return fmt.Errorf("chown %s: %w", path, err)
	}
	return nil
}

func runFileValidation(validation *fileValidationSpec, path string) error {
	args := append([]string(nil), validation.Argv...)
	if validation.FileAsArg {
		args = append(args, path)
	}
	result, err := pluginsdk.CmdExec(args[0], args[1:])
	if err != nil {
		return fmt.Errorf("run validation command %q: %w", strings.Join(args, " "), err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("validation command %q failed (%s)", strings.Join(args, " "), commandFailureDetail(result))
	}
	return nil
}

func fileValidationTempPath(path, kind string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	return filepath.Join(dir, fmt.Sprintf(".%s.tf-linux-provider-%s-%d", base, kind, time.Now().UnixNano()))
}

func moveManagedFilePath(from, to string) error {
	if err := pluginsdk.FileRename(from, to); err != nil {
		return fmt.Errorf("move %s to %s: %w", from, to, err)
	}
	return nil
}

func removeManagedFilePath(path string) error {
	if err := pluginsdk.FileDelete(path); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

func rollbackValidatedFile(path, backupPath string) error {
	if err := removeManagedFilePath(path); err != nil {
		return err
	}
	if backupPath == "" {
		return nil
	}
	return moveManagedFilePath(backupPath, path)
}

func buildKernelModulesState(path string, modules []string) pluginsdk.StateData {
	return pluginsdk.StateData{
		"id":      path,
		"path":    path,
		"modules": modules,
	}
}

func buildSwapState(enabled bool) pluginsdk.StateData {
	return pluginsdk.StateData{
		"id":      "system",
		"enabled": enabled,
	}
}

func buildFileState(plan pluginsdk.StateData, stat *pluginsdk.FileStat, owner, group, mode string) pluginsdk.StateData {
	path := plan.GetString("path")
	out := pluginsdk.StateData{
		"id":        path,
		"path":      path,
		"sensitive": fileStateSensitive(plan),
		"owner":     owner,
		"group":     group,
		"mode":      mode,
		"digest":    stat.Digest,
	}
	if _, ok := plan["content"]; ok {
		out["content"] = plannedContentStateValue(plan, stat.Digest, false)
	}
	if _, ok := plan["content_base64"]; ok {
		out["content_base64"] = plannedContentStateValue(plan, stat.Digest, true)
	}
	if v := plan.GetString("run_as"); v != "" {
		out["run_as"] = v
	}
	if validation, err := fileValidationStateValue(plan); err == nil && validation != nil {
		out["validation"] = validation
	}
	return out
}

func normalizeKernelModules(modules []string) ([]string, error) {
	seen := make(map[string]bool, len(modules))
	result := make([]string, 0, len(modules))
	for _, module := range modules {
		module = strings.TrimSpace(module)
		if module == "" {
			continue
		}
		if strings.ContainsAny(module, " \t\n\r") {
			return nil, fmt.Errorf("kernel module name %q must not contain whitespace", module)
		}
		if seen[module] {
			continue
		}
		seen[module] = true
		result = append(result, module)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("modules must contain at least one kernel module name")
	}
	return result, nil
}

func parseKernelModulesContent(content string) []string {
	seen := map[string]bool{}
	result := make([]string, 0)
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		module := fields[0]
		if seen[module] {
			continue
		}
		seen[module] = true
		result = append(result, module)
	}
	return result
}

func serializeKernelModules(modules []string) string {
	if len(modules) == 0 {
		return ""
	}
	return strings.Join(modules, "\n") + "\n"
}

func loadKernelModule(module string) error {
	result, err := pluginsdk.CmdExec("modprobe", []string{module})
	if err != nil {
		return fmt.Errorf("load kernel module %s: %w", module, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("load kernel module %s failed (%s)", module, commandFailureDetail(result))
	}
	return nil
}

func isNotExistError(err error) bool {
	if err == nil {
		return false
	}
	if os.IsNotExist(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such file or directory") || strings.Contains(msg, "file does not exist") || strings.Contains(msg, "not found")
}

func parseOctal(s string) (uint32, error) {
	var mode uint32
	_, err := fmt.Sscanf(s, "%o", &mode)
	return mode, err
}

func withDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func withDefaultList(values, def []string) []string {
	if len(values) == 0 {
		return def
	}
	return values
}

func ensureParentDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "" || dir == "." || dir == "/" {
		return nil
	}
	stat, err := pluginsdk.FileStat_(dir)
	if err == nil {
		if !stat.IsDir {
			return fmt.Errorf("parent path %s exists but is not a directory", dir)
		}
		return nil
	}
	if !isNotExistError(err) {
		return fmt.Errorf("stat parent directory %s: %w", dir, err)
	}
	if err := pluginsdk.DirEnsure(dir, 0o755); err != nil {
		return fmt.Errorf("create parent directory %s: %w", dir, err)
	}
	return nil
}

func isValidHostToken(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, " \t#") {
		return false
	}
	return true
}

func normalizeHostAliases(hostname string, aliases []string) []string {
	seen := map[string]bool{hostname: true}
	out := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" || seen[alias] {
			continue
		}
		seen[alias] = true
		out = append(out, alias)
	}
	return out
}

func serializeHostsComment(comment string) string {
	comment = strings.TrimSpace(comment)
	if comment == "" {
		return ""
	}
	comment = strings.TrimPrefix(comment, "#")
	comment = strings.TrimSpace(comment)
	if comment == "" {
		return ""
	}
	return "# " + comment
}

func hostsCommentState(comment string) string {
	comment = strings.TrimSpace(comment)
	comment = strings.TrimPrefix(comment, "#")
	return strings.TrimSpace(comment)
}

func upsertHostsEntry(entries []pluginsdk.HostEntry, desired pluginsdk.HostEntry) []pluginsdk.HostEntry {
	updated := make([]pluginsdk.HostEntry, 0, len(entries)+1)
	matched := false
	for _, entry := range entries {
		if entry.IP == desired.IP && entry.Hostname == desired.Hostname {
			if !matched {
				updated = append(updated, desired)
				matched = true
			}
			continue
		}
		updated = append(updated, entry)
	}
	if !matched {
		updated = append(updated, desired)
	}
	return updated
}

func fileStateSensitive(values pluginsdk.StateData) bool {
	if _, ok := values["sensitive"]; !ok {
		return true
	}
	return values.GetBool("sensitive")
}

func plannedContentStateValue(plan pluginsdk.StateData, digest string, isBase64 bool) string {
	if fileStateSensitive(plan) {
		return digest
	}
	if isBase64 {
		return plan.GetString("content_base64")
	}
	return plan.GetString("content")
}

func fileStateContentValue(state pluginsdk.StateData, digest string, isBase64 bool) string {
	if fileStateSensitive(state) {
		return digest
	}

	content, err := pluginsdk.FileRead(state.GetString("path"))
	if err != nil {
		return digest
	}
	if isBase64 {
		return base64.StdEncoding.EncodeToString(content)
	}
	return string(content)
}

func hostsEntryID(ip, hostname string) string {
	return ip + "/" + hostname
}

func commandFailureDetail(result *pluginsdk.CmdResult) string {
	return pluginsdk.CommandFailureDetail(result)
}

func init() {
	pluginsdk.RegisterResource(&fileResource{})
	pluginsdk.RegisterResource(&symlinkResource{})
	pluginsdk.RegisterResource(&kernelModulesResource{})
	pluginsdk.RegisterResource(&swapResource{})
	pluginsdk.RegisterDataSource(&fileInfoDataSource{})
	pluginsdk.RegisterResource(&hostsEntryResource{})
	pluginsdk.RegisterResource(&crontabEntryResource{})
	pluginsdk.RegisterResource(&sysctlEntryResource{})
	pluginsdk.RegisterResource(&networkStackResource{})
	pluginsdk.RegisterResource(&fstabEntryResource{})
	pluginsdk.RegisterResource(&sshdConfigResource{})
}

func main() {}
