// Copyright IBM Corp. 2026

package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/aptkeyring"
	linuxpackagescontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/linuxpackages"
)

type packageResource struct{}

type packageLockResource struct{}

var (
	packageRetryMaxAttempts = 6
	packageRetryBaseDelay   = 2 * time.Second
	packageRetrySleep       = time.Sleep
	yumVersionlockRe        = regexp.MustCompile(`^(?:!)?(?P<epoch>\d+):(?P<name>.+)-(?P<version>.+)-(?P<release>.+)\.(?P<arch>.+)$`)
	dnfVersionlockRe        = regexp.MustCompile(`^(?:!)?(?P<name>.+)-(?P<epoch>\d+):(?P<version>.+)-(?P<release>.+)\.(?P<arch>.+)$`)
)

func (r *packageResource) Name() string { return "package" }

func (r *packageLockResource) Name() string { return "package_lock" }

func (r *packageResource) Schema() pluginsdk.Schema {
	return linuxpackagescontract.PackageResourceSchema()
}

func (r *packageLockResource) Schema() pluginsdk.Schema {
	return linuxpackagescontract.PackageLockResourceSchema()
}

func (r *packageResource) Validate(config pluginsdk.StateData) error {
	name := config.GetString("name")
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("name must not be empty")
	}
	ensure := packageEnsure(config)
	if ensure != "present" && ensure != "latest" && ensure != "absent" {
		return fmt.Errorf("ensure must be \"present\", \"latest\", or \"absent\", got %q", ensure)
	}
	if config.GetString("version") != "" && ensure == "absent" {
		return fmt.Errorf("version cannot be set when ensure is \"absent\"")
	}
	if config.GetString("version") != "" && ensure == "latest" {
		return fmt.Errorf("version cannot be set when ensure is \"latest\"")
	}
	repoKeyringPath := strings.TrimSpace(config.GetString("repo_keyring_path"))
	repoKeyringURL := strings.TrimSpace(config.GetString("repo_keyring_url"))
	if repoKeyringPath == "" && repoKeyringURL == "" {
		return nil
	}
	if repoKeyringPath == "" || repoKeyringURL == "" {
		return fmt.Errorf("repo_keyring_path and repo_keyring_url must be set together")
	}
	if !filepath.IsAbs(repoKeyringPath) {
		return fmt.Errorf("repo_keyring_path must be an absolute path, got %q", repoKeyringPath)
	}
	return nil
}

func (r *packageLockResource) Validate(config pluginsdk.StateData) error {
	packages, err := normalizePackageNames(config.GetStringList("packages"))
	if err != nil {
		return err
	}
	if len(packages) == 0 {
		return fmt.Errorf("packages must contain at least one package name")
	}
	ensure := packageLockEnsure(config)
	if ensure != "present" && ensure != "absent" {
		return fmt.Errorf("ensure must be \"present\" or \"absent\", got %q", ensure)
	}
	return nil
}

func (r *packageResource) Read(state pluginsdk.StateData) (pluginsdk.StateData, error) {
	name := state.GetString("name")
	if name == "" {
		return nil, nil
	}
	ensure := packageEnsure(state)
	updateCache := state.GetBool("update_cache")
	pm, err := pluginsdk.HostPackageManager()
	if err != nil {
		return nil, fmt.Errorf("detect package manager: %w", err)
	}
	version, err := queryPackage(pm, name)
	if err != nil {
		return nil, err
	}
	if version == "" {
		if ensure == "absent" {
			return buildPackageState(nil, state, "", "absent", updateCache), nil
		}
		return nil, nil
	}
	if repoKeyringConfigured(state) {
		exists, err := packageRepoKeyringExists(state)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, nil
		}
	}
	return buildPackageState(nil, state, version, ensure, updateCache), nil
}

func (r *packageLockResource) Read(state pluginsdk.StateData) (pluginsdk.StateData, error) {
	desired, err := normalizePackageNames(state.GetStringList("packages"))
	if err != nil {
		return nil, err
	}
	if len(desired) == 0 {
		return nil, nil
	}
	ensure := packageLockEnsure(state)
	pm, err := pluginsdk.HostPackageManager()
	if err != nil {
		return nil, fmt.Errorf("detect package manager: %w", err)
	}
	locked, err := currentPackageLocks(pm, desired)
	if err != nil {
		return nil, err
	}
	if ensure == "absent" {
		return buildPackageLockState(desired, ensure, desired), nil
	}
	current := make([]string, 0, len(desired))
	for _, pkg := range desired {
		if locked[pkg] {
			current = append(current, pkg)
		}
	}
	if len(current) == 0 {
		return nil, nil
	}
	return buildPackageLockState(desired, ensure, current), nil
}

func (r *packageResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applyPackage(nil, plan)
}

func (r *packageLockResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applyPackageLock(nil, plan)
}

func (r *packageResource) Update(prior, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applyPackage(prior, plan)
}

func (r *packageLockResource) Update(prior, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applyPackageLock(prior, plan)
}

func applyPackage(prior, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	name := plan.GetString("name")
	ensure := packageEnsure(plan)
	updateCache := plan.GetBool("update_cache")
	pm, err := pluginsdk.HostPackageManager()
	if err != nil {
		return nil, fmt.Errorf("detect package manager: %w", err)
	}
	if ensure == "absent" {
		if err := removePackage(pm, name); err != nil {
			return nil, err
		}
		if err := cleanupPackageRepoKeyring(prior, plan); err != nil {
			return nil, err
		}
		return buildPackageState(prior, plan, "", "absent", updateCache), nil
	}
	if updateCache {
		if err := refreshPackageCache(pm); err != nil {
			return nil, err
		}
	}
	requestedVersion := strings.TrimSpace(plan.GetString("version"))
	currentVersion, err := queryPackage(pm, name)
	if err != nil {
		return nil, err
	}
	if ensure == "present" && currentVersion != "" {
		if requestedVersion == "" || requestedVersion == currentVersion {
			if err := ensurePackageRepoKeyring(pm, plan); err != nil {
				return nil, err
			}
			if err := cleanupPackageRepoKeyring(prior, plan); err != nil {
				return nil, err
			}
			return buildPackageState(prior, plan, currentVersion, "present", updateCache), nil
		}
	}
	if err := installPackage(pm, name, requestedVersion); err != nil {
		return nil, err
	}
	if err := ensurePackageRepoKeyring(pm, plan); err != nil {
		return nil, err
	}
	if err := cleanupPackageRepoKeyring(prior, plan); err != nil {
		return nil, err
	}
	version, err := queryPackage(pm, name)
	if err != nil {
		return nil, err
	}
	return buildPackageState(prior, plan, version, ensure, updateCache), nil
}

func (r *packageResource) Delete(state pluginsdk.StateData) error {
	pm, err := pluginsdk.HostPackageManager()
	if err != nil {
		return fmt.Errorf("detect package manager: %w", err)
	}
	if err := removePackage(pm, state.GetString("name")); err != nil {
		return err
	}
	return cleanupPackageRepoKeyring(state, pluginsdk.StateData{"ensure": "absent", "repo_keyring_path": state.GetString("repo_keyring_path")})
}

func (r *packageLockResource) Delete(state pluginsdk.StateData) error {
	packages, err := normalizePackageNames(state.GetStringList("packages"))
	if err != nil {
		return err
	}
	if len(packages) == 0 {
		return nil
	}
	pm, err := pluginsdk.HostPackageManager()
	if err != nil {
		return fmt.Errorf("detect package manager: %w", err)
	}
	return changePackageLocks(pm, packages, "absent")
}

func (r *packageResource) ImportState(id string) (pluginsdk.StateData, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("import ID (package name) must not be empty")
	}
	return r.Read(pluginsdk.StateData{"name": id, "ensure": "present"})
}

func (r *packageLockResource) ImportState(id string) (pluginsdk.StateData, error) {
	packages, err := normalizePackageNames(strings.Split(id, ","))
	if err != nil {
		return nil, err
	}
	if len(packages) == 0 {
		return nil, fmt.Errorf("import ID must contain at least one package name")
	}
	return r.Read(pluginsdk.StateData{"packages": packages, "ensure": "present"})
}

func applyPackageLock(prior, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	desired, err := normalizePackageNames(plan.GetStringList("packages"))
	if err != nil {
		return nil, err
	}
	ensure := packageLockEnsure(plan)
	pm, err := pluginsdk.HostPackageManager()
	if err != nil {
		return nil, fmt.Errorf("detect package manager: %w", err)
	}

	managed := make([]string, 0, len(desired)+len(prior.GetStringList("packages")))
	managed = append(managed, prior.GetStringList("packages")...)
	managed = append(managed, desired...)
	managed, err = normalizePackageNames(managed)
	if err != nil {
		return nil, err
	}

	locked, err := currentPackageLocks(pm, managed)
	if err != nil {
		return nil, err
	}

	if ensure == "present" {
		if err := changePackageLocks(pm, missingPackages(desired, locked), "present"); err != nil {
			return nil, err
		}
		if err := changePackageLocks(pm, packagesToUnlock(prior.GetStringList("packages"), desired, locked), "absent"); err != nil {
			return nil, err
		}
		return buildPackageLockState(desired, ensure, desired), nil
	}

	if err := changePackageLocks(pm, presentPackages(managed, locked), "absent"); err != nil {
		return nil, err
	}
	return buildPackageLockState(desired, ensure, desired), nil
}

func buildPackageState(prior, plan pluginsdk.StateData, version, ensure string, updateCache bool) pluginsdk.StateData {
	state := pluginsdk.StateData{
		"id":           plan.GetString("name"),
		"name":         plan.GetString("name"),
		"version":      version,
		"ensure":       ensure,
		"update_cache": updateCache,
	}
	preserveNullableString(state, prior, plan, "repo_keyring_path")
	preserveNullableString(state, prior, plan, "repo_keyring_url")
	return state
}

func ensurePackageRepoKeyring(pm string, plan pluginsdk.StateData) error {
	path := packageRepoKeyringPath(plan)
	url := packageRepoKeyringURL(plan)
	if path == "" && url == "" {
		return nil
	}
	if path == "" || url == "" {
		return fmt.Errorf("repo_keyring_path and repo_keyring_url must be set together")
	}
	if pm != "apt" {
		return fmt.Errorf("repo_keyring_path and repo_keyring_url are only supported for apt-managed hosts")
	}
	if err := aptkeyring.Install(url, path); err != nil {
		return fmt.Errorf("install repo keyring %s: %w", path, err)
	}
	return nil
}

func cleanupPackageRepoKeyring(prior, plan pluginsdk.StateData) error {
	paths := make([]string, 0, 2)
	if packageEnsure(plan) == "absent" {
		paths = append(paths, packageRepoKeyringPath(prior), packageRepoKeyringPath(plan))
	} else {
		oldPath := packageRepoKeyringPath(prior)
		newPath := packageRepoKeyringPath(plan)
		if oldPath != "" && oldPath != newPath {
			paths = append(paths, oldPath)
		}
	}
	seen := map[string]bool{}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		if err := removePackageRepoKeyring(path); err != nil {
			return err
		}
	}
	return nil
}

func removePackageRepoKeyring(path string) error {
	referenced, err := aptkeyring.Referenced(path)
	if err != nil {
		return fmt.Errorf("check repo keyring references for %s: %w", path, err)
	}
	if referenced {
		return nil
	}
	if err := pluginsdk.FileDelete(path); err != nil {
		return fmt.Errorf("remove repo keyring %s: %w", path, err)
	}
	return nil
}

func packageRepoKeyringExists(state pluginsdk.StateData) (bool, error) {
	path := packageRepoKeyringPath(state)
	if path == "" {
		return true, nil
	}
	stat, err := pluginsdk.FileStat_(path)
	if err != nil {
		if isNotExistError(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat repo keyring %s: %w", path, err)
	}
	if stat.IsDir {
		return false, fmt.Errorf("repo keyring path %s is a directory", path)
	}
	return true, nil
}

func repoKeyringConfigured(data pluginsdk.StateData) bool {
	return packageRepoKeyringPath(data) != "" || packageRepoKeyringURL(data) != ""
}

func packageRepoKeyringPath(data pluginsdk.StateData) string {
	return strings.TrimSpace(data.GetString("repo_keyring_path"))
}

func packageRepoKeyringURL(data pluginsdk.StateData) string {
	return strings.TrimSpace(data.GetString("repo_keyring_url"))
}

func preserveNullableString(state pluginsdk.StateData, prior pluginsdk.StateData, config pluginsdk.StateData, key string) {
	if value, ok := config[key]; ok {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
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

func isNotExistError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no such file or directory") || strings.Contains(text, "not found")
}

func buildPackageLockState(idPackages []string, ensure string, statePackages []string) pluginsdk.StateData {
	return pluginsdk.StateData{
		"id":       packageLockID(idPackages),
		"packages": statePackages,
		"ensure":   ensure,
	}
}

func currentPackageLocks(pm string, packages []string) (map[string]bool, error) {
	packages, err := normalizePackageNames(packages)
	if err != nil {
		return nil, err
	}
	locked := make(map[string]bool, len(packages))
	if len(packages) == 0 {
		return locked, nil
	}

	switch pm {
	case "apt":
		result, err := pluginsdk.CmdExec("apt-mark", []string{"showhold"})
		if err != nil {
			return nil, fmt.Errorf("list apt holds: %w", err)
		}
		if result.ExitCode != 0 {
			return nil, fmt.Errorf("list apt holds failed (%s)", commandFailureDetail(result))
		}
		held := make(map[string]bool)
		for _, line := range strings.Split(result.Stdout, "\n") {
			name := strings.TrimSpace(line)
			if name != "" {
				held[name] = true
			}
		}
		for _, pkg := range packages {
			locked[pkg] = held[pkg]
		}
		return locked, nil
	case "dnf", "yum":
		entries, err := listRPMVersionlockEntries(pm)
		if err != nil {
			return nil, err
		}
		for _, pkg := range packages {
			for _, entry := range entries {
				if rpmVersionlockMatches(entry, pkg) {
					locked[pkg] = true
					break
				}
			}
		}
		return locked, nil
	default:
		return nil, fmt.Errorf("package_lock is not supported for package manager %q", pm)
	}
}

func changePackageLocks(pm string, packages []string, ensure string) error {
	packages, err := normalizePackageNames(packages)
	if err != nil {
		return err
	}
	if len(packages) == 0 {
		return nil
	}

	switch pm {
	case "apt":
		action := "hold"
		description := fmt.Sprintf("hold packages %q", strings.Join(packages, ", "))
		if ensure == "absent" {
			action = "unhold"
			description = fmt.Sprintf("unhold packages %q", strings.Join(packages, ", "))
		}
		return runPackageCommand(pm, description, "apt-mark", append([]string{action}, packages...))
	case "dnf", "yum":
		action := "add"
		verb := "add"
		if ensure == "absent" {
			action = "delete"
			verb = "remove"
		}
		for _, pkg := range packages {
			if err := runPackageCommand(pm, fmt.Sprintf("%s package lock %q", verb, pkg), pm, []string{"-q", "versionlock", action, pkg}); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("package_lock is not supported for package manager %q", pm)
	}
}

func listRPMVersionlockEntries(pm string) ([]string, error) {
	result, err := pluginsdk.CmdExec(pm, []string{"versionlock", "list"})
	if err != nil {
		return nil, fmt.Errorf("list %s package locks: %w", pm, err)
	}
	if result.ExitCode != 0 {
		detail := commandFailureDetail(result)
		if strings.Contains(strings.ToLower(detail), "no such command") {
			return nil, fmt.Errorf("versionlock plugin is required for %s package locking (%s)", pm, detail)
		}
		return nil, fmt.Errorf("list %s package locks failed (%s)", pm, detail)
	}

	entries := make([]string, 0)
	var dnf5Package string
	for _, line := range strings.Split(result.Stdout, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "Package name:") {
			dnf5Package = strings.TrimSpace(strings.TrimPrefix(trimmed, "Package name:"))
			continue
		}
		if dnf5Package != "" && strings.HasPrefix(trimmed, "evr") {
			parts := strings.SplitN(trimmed, "=", 2)
			if len(parts) == 2 {
				entries = append(entries, dnf5Package+"-"+strings.TrimSpace(parts[1])+".*")
			}
			dnf5Package = ""
			continue
		}
		entries = append(entries, strings.Fields(trimmed)...)
	}
	return entries, nil
}

func rpmVersionlockMatches(entry, name string) bool {
	entry = strings.TrimSpace(strings.TrimPrefix(entry, "!"))
	name = strings.TrimSpace(name)
	if entry == name || strings.TrimSuffix(entry, ".*") == name {
		return true
	}
	for _, re := range []*regexp.Regexp{yumVersionlockRe, dnfVersionlockRe} {
		matches := re.FindStringSubmatch(entry)
		if matches == nil {
			continue
		}
		groups := map[string]string{}
		for idx, group := range re.SubexpNames() {
			if idx > 0 && group != "" {
				groups[group] = matches[idx]
			}
		}
		candidate := groups["name"]
		if candidate == name {
			return true
		}
	}
	return false
}

func queryPackage(pm, name string) (string, error) {
	var cmd string
	var args []string
	switch pm {
	case "apt":
		cmd = "dpkg-query"
		args = []string{"-W", "-f=${db:Status-Abbrev}\t${Version}", name}
	case "dnf", "yum":
		cmd = "rpm"
		args = []string{"-q", "--queryformat", "%{VERSION}-%{RELEASE}", name}
	case "apk":
		res, err := pluginsdk.CmdExec("apk", []string{"info", "-e", name})
		if err != nil {
			return "", fmt.Errorf("apk info: %w", err)
		}
		if res.ExitCode != 0 {
			return "", nil
		}
		cmd = "apk"
		args = []string{"version", name}
	case "pacman":
		cmd = "pacman"
		args = []string{"-Q", name}
	case "zypper":
		cmd = "rpm"
		args = []string{"-q", "--queryformat", "%{VERSION}", name}
	default:
		return "", fmt.Errorf("unsupported package manager: %q", pm)
	}
	res, err := pluginsdk.CmdExec(cmd, args)
	if err != nil {
		return "", fmt.Errorf("query package %q: %w", name, err)
	}
	if res.ExitCode != 0 {
		return "", nil
	}
	version := strings.TrimSpace(res.Stdout)
	if pm == "apt" {
		fields := strings.Fields(version)
		if len(fields) < 2 || fields[0] != "ii" {
			return "", nil
		}
		version = fields[1]
	}
	if pm == "pacman" {
		parts := strings.Fields(version)
		if len(parts) >= 2 {
			version = parts[1]
		}
	}
	if pm == "apk" {
		for _, line := range strings.Split(version, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, name+"-") {
				rest := strings.TrimPrefix(line, name+"-")
				if idx := strings.IndexAny(rest, " \t"); idx != -1 {
					rest = rest[:idx]
				}
				version = rest
				break
			}
		}
	}
	return version, nil
}

func installPackage(pm, name, version string) error {
	var cmd string
	var args []string
	spec, err := packageSpec(pm, name, version)
	if err != nil {
		return err
	}
	switch pm {
	case "apt":
		cmd = "env"
		args = []string{"DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "--allow-change-held-packages", spec}
	case "dnf":
		cmd = "dnf"
		args = []string{"install", "-y", spec}
	case "yum":
		cmd = "yum"
		args = []string{"install", "-y", spec}
	case "apk":
		cmd = "apk"
		args = []string{"add", spec}
	case "pacman":
		cmd = "pacman"
		args = []string{"-S", "--noconfirm", spec}
	case "zypper":
		cmd = "zypper"
		args = []string{"install", "-y", spec}
	default:
		return fmt.Errorf("unsupported package manager: %q", pm)
	}
	return runPackageCommand(pm, fmt.Sprintf("install package %q", spec), cmd, args)
}

func removePackage(pm, name string) error {
	installedVersion, err := queryPackage(pm, name)
	if err != nil {
		return err
	}
	if installedVersion == "" {
		return nil
	}
	var cmd string
	var args []string
	switch pm {
	case "apt":
		cmd = "env"
		args = []string{"DEBIAN_FRONTEND=noninteractive", "apt-get", "purge", "-y", "--allow-change-held-packages", name}
	case "dnf":
		cmd = "dnf"
		args = []string{"remove", "-y", name}
	case "yum":
		cmd = "yum"
		args = []string{"remove", "-y", name}
	case "apk":
		cmd = "apk"
		args = []string{"del", name}
	case "pacman":
		cmd = "pacman"
		args = []string{"-R", "--noconfirm", name}
	case "zypper":
		cmd = "zypper"
		args = []string{"remove", "-y", name}
	default:
		return fmt.Errorf("unsupported package manager: %q", pm)
	}
	return runPackageCommand(pm, fmt.Sprintf("remove package %q", name), cmd, args)
}

func refreshPackageCache(pm string) error {
	var cmd string
	var args []string
	switch pm {
	case "apt":
		cmd = "env"
		args = []string{"DEBIAN_FRONTEND=noninteractive", "apt-get", "update"}
	case "dnf":
		cmd = "dnf"
		args = []string{"makecache", "-y"}
	case "yum":
		cmd = "yum"
		args = []string{"makecache", "-y"}
	case "apk":
		cmd = "apk"
		args = []string{"update"}
	case "pacman":
		cmd = "pacman"
		args = []string{"-Sy", "--noconfirm"}
	case "zypper":
		cmd = "zypper"
		args = []string{"refresh", "-y"}
	default:
		return fmt.Errorf("unsupported package manager: %q", pm)
	}
	return runPackageCommand(pm, fmt.Sprintf("refresh package cache for %q", pm), cmd, args)
}

func runPackageCommand(pm, description, cmd string, args []string) error {
	if pm == "apt" {
		return runAptCommandWithRetry(description, cmd, args)
	}

	res, err := pluginsdk.CmdExec(cmd, args)
	if err != nil {
		return fmt.Errorf("%s: %w", description, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s failed (%s)", description, commandFailureDetail(res))
	}
	return nil
}

func runAptCommandWithRetry(description, cmd string, args []string) error {
	return pluginsdk.RetryCommand(description, cmd, args, pluginsdk.CommandRetryPolicy{
		MaxAttempts: packageRetryMaxAttempts,
		BaseDelay:   packageRetryBaseDelay,
		Sleep:       packageRetrySleep,
		IsTransient: isTransientAptBusy,
		OnRetry: func(delay time.Duration, detail string) {
			pluginsdk.LogInfo(fmt.Sprintf("%s hit transient apt/dpkg contention; retrying in %s: %s", description, delay, detail))
		},
	})
}

func packageRetryBackoff(attempt int) time.Duration {
	return pluginsdk.ExponentialBackoff(packageRetryBaseDelay, attempt)
}

func isTransientAptBusy(detail string) bool {
	return pluginsdk.IsTransientAptBusy(detail)
}

func commandFailureDetail(result *pluginsdk.CmdResult) string {
	return pluginsdk.CommandFailureDetail(result)
}

func packageSpec(pm, name, version string) (string, error) {
	if version == "" {
		return name, nil
	}
	switch pm {
	case "apt", "zypper":
		return name + "=" + version, nil
	case "dnf", "yum":
		return name + "-" + version, nil
	case "apk":
		return name + "=" + version, nil
	case "pacman":
		return "", fmt.Errorf("exact version installs are not supported for pacman in package")
	default:
		return "", fmt.Errorf("unsupported package manager: %q", pm)
	}
}

func normalizePackageNames(packages []string) ([]string, error) {
	seen := make(map[string]bool, len(packages))
	result := make([]string, 0, len(packages))
	for _, pkg := range packages {
		pkg = strings.TrimSpace(pkg)
		if pkg == "" {
			continue
		}
		if strings.ContainsAny(pkg, " \t\n\r") {
			return nil, fmt.Errorf("package name %q must not contain whitespace", pkg)
		}
		if seen[pkg] {
			continue
		}
		seen[pkg] = true
		result = append(result, pkg)
	}
	return result, nil
}

func withDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func packageEnsure(data pluginsdk.StateData) string {
	return withDefault(data.GetString("ensure"), "present")
}

func packageLockEnsure(data pluginsdk.StateData) string {
	return withDefault(data.GetString("ensure"), "present")
}

func packageLockID(packages []string) string {
	packages, _ = normalizePackageNames(packages)
	copyPackages := append([]string(nil), packages...)
	for i := 0; i < len(copyPackages); i++ {
		for j := i + 1; j < len(copyPackages); j++ {
			if copyPackages[j] < copyPackages[i] {
				copyPackages[i], copyPackages[j] = copyPackages[j], copyPackages[i]
			}
		}
	}
	return strings.Join(copyPackages, ",")
}

func missingPackages(desired []string, locked map[string]bool) []string {
	result := make([]string, 0, len(desired))
	for _, pkg := range desired {
		if !locked[pkg] {
			result = append(result, pkg)
		}
	}
	return result
}

func presentPackages(packages []string, locked map[string]bool) []string {
	result := make([]string, 0, len(packages))
	for _, pkg := range packages {
		if locked[pkg] {
			result = append(result, pkg)
		}
	}
	return result
}

func packagesToUnlock(prior, desired []string, locked map[string]bool) []string {
	prior, _ = normalizePackageNames(prior)
	desired, _ = normalizePackageNames(desired)
	desiredSet := make(map[string]bool, len(desired))
	for _, pkg := range desired {
		desiredSet[pkg] = true
	}
	result := make([]string, 0, len(prior))
	for _, pkg := range prior {
		if desiredSet[pkg] || !locked[pkg] {
			continue
		}
		result = append(result, pkg)
	}
	return result
}

func init() {
	pluginsdk.RegisterResource(&packageResource{})
	pluginsdk.RegisterResource(&packageLockResource{})
}

func main() {}
