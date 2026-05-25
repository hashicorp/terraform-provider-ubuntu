// Copyright IBM Corp. 2026

package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/aptkeyring"
	debianaptcontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/debianapt"
)

const aptSourcesDir = "/etc/apt/sources.list.d"

type aptRepositoryResource struct{}

var (
	aptRetryMaxAttempts = 6
	aptRetryBaseDelay   = 2 * time.Second
	aptRetrySleep       = time.Sleep
)

type aptRepositorySpec struct {
	Name           string
	URI            string
	Suite          string
	Components     []string
	Architectures  []string
	SignedBy       string
	SignedByKeyURL string
	State          string
	FilePath       string
	SourceLine     string
}

func (r *aptRepositoryResource) Name() string { return "apt_repository" }

func (r *aptRepositoryResource) Schema() pluginsdk.Schema {
	return debianaptcontract.AptRepositoryResourceSchema()
}

func (r *aptRepositoryResource) Validate(config pluginsdk.StateData) error {
	if err := ensureDebianAPT(); err != nil {
		return err
	}

	ensure := repositoryEnsure(config)
	if ensure != "present" && ensure != "absent" {
		return fmt.Errorf("ensure must be \"present\" or \"absent\", got %q", ensure)
	}
	if strings.TrimSpace(config.GetString("uri")) == "" {
		return fmt.Errorf("uri must not be empty")
	}
	if ensure == "absent" {
		return nil
	}
	if keyURL := signedByKeyURL(config); keyURL != "" {
		signedBy := strings.TrimSpace(config.GetString("signed_by"))
		if signedBy == "" {
			return fmt.Errorf("signed_by must be set when signed_by_key_url is provided")
		}
		if !filepath.IsAbs(signedBy) {
			return fmt.Errorf("signed_by must be an absolute path when signed_by_key_url is provided, got %q", signedBy)
		}
	}

	path, err := repositoryFilePath(config)
	if err != nil {
		return err
	}
	if _, err := desiredRepositorySpec(config, path); err != nil {
		return err
	}

	return nil
}

func (r *aptRepositoryResource) Read(state pluginsdk.StateData) (pluginsdk.StateData, error) {
	if err := ensureDebianAPT(); err != nil {
		return nil, err
	}

	path, err := repositoryFilePath(state)
	if err != nil {
		return nil, err
	}

	exists, err := pluginsdk.FileExists(path)
	if err != nil {
		return nil, fmt.Errorf("check repository file %s: %w", path, err)
	}
	if !exists {
		if repositoryEnsure(state) == "absent" {
			return absentState(state, path), nil
		}
		return nil, nil
	}

	readState, err := readRepositoryAtPath(path)
	if err != nil {
		return nil, err
	}
	readState["update_cache"] = state.GetBool("update_cache")
	readState["signed_by_key_url"] = preservedSignedByKeyURL(state, readState)
	return readState, nil
}

func (r *aptRepositoryResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	if err := ensureDebianAPT(); err != nil {
		return nil, err
	}

	path, err := repositoryFilePath(plan)
	if err != nil {
		return nil, err
	}

	exists, err := pluginsdk.FileExists(path)
	if err != nil {
		return nil, fmt.Errorf("check repository file %s: %w", path, err)
	}
	if exists {
		adopted, err := adoptExistingRepository(plan, path)
		if err != nil {
			return nil, err
		}
		if adopted != nil {
			return adopted, nil
		}
		return nil, fmt.Errorf("apt repository %q already exists at %s; import it before managing with terraform", repositoryName(plan), path)
	}

	return applyRepository(nil, plan)
}

func (r *aptRepositoryResource) Update(prior pluginsdk.StateData, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applyRepository(prior, plan)
}

func (r *aptRepositoryResource) Delete(state pluginsdk.StateData) error {
	if err := ensureDebianAPT(); err != nil {
		return err
	}
	path, err := repositoryFilePath(state)
	if err != nil {
		return err
	}
	if _, err := removeRepositoryFile(path); err != nil {
		return err
	}
	return cleanupRepositoryKeyrings(state, pluginsdk.StateData{
		"ensure":            "absent",
		"signed_by":         state.GetString("signed_by"),
		"signed_by_key_url": state.GetString("signed_by_key_url"),
	})
}

func (r *aptRepositoryResource) ImportState(id string) (pluginsdk.StateData, error) {
	if err := ensureDebianAPT(); err != nil {
		return nil, err
	}

	path := strings.TrimSpace(id)
	if path == "" {
		return nil, fmt.Errorf("import ID must be a repository file path or basename")
	}
	if !strings.HasPrefix(path, "/") {
		path = filepath.Join(aptSourcesDir, sanitizeRepoToken(path)+".list")
	}

	return readRepositoryAtPath(path)
}

func applyRepository(prior, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	if err := ensureDebianAPT(); err != nil {
		return nil, err
	}

	path, err := repositoryFilePath(plan)
	if err != nil {
		return nil, err
	}

	ensure := repositoryEnsure(plan)
	if ensure == "absent" {
		changed, err := removeRepositoryFile(path)
		if err != nil {
			return nil, err
		}
		if changed && plan.GetBool("update_cache") {
			if err := aptUpdate(); err != nil {
				return nil, err
			}
		}
		if err := cleanupRepositoryKeyrings(prior, plan); err != nil {
			return nil, err
		}
		return absentState(plan, path), nil
	}
	if err := ensureRepositoryKeyring(plan); err != nil {
		return nil, err
	}

	spec, err := desiredRepositorySpec(plan, path)
	if err != nil {
		return nil, err
	}

	content := renderRepositoryFile(spec)
	changed, err := writeRepositoryFile(path, content)
	if err != nil {
		return nil, err
	}
	if changed && plan.GetBool("update_cache") {
		if err := aptUpdate(); err != nil {
			return nil, err
		}
	}
	if err := cleanupRepositoryKeyrings(prior, plan); err != nil {
		return nil, err
	}

	state := specToState(spec)
	state["update_cache"] = plan.GetBool("update_cache")
	return state, nil
}

func desiredRepositorySpec(plan pluginsdk.StateData, path string) (*aptRepositorySpec, error) {
	name := repositoryName(plan)
	if name == "" {
		return nil, fmt.Errorf("repository name could not be derived from uri")
	}
	suite := strings.TrimSpace(plan.GetString("suite"))
	if suite == "" {
		defaultSuite, err := defaultDebianSuite()
		if err != nil {
			return nil, err
		}
		suite = defaultSuite
	}
	components := normalizeStrings(plan.GetStringList("components"))
	if !strings.HasSuffix(suite, "/") && len(components) == 0 {
		return nil, fmt.Errorf("components must not be empty when suite %q is not an exact path", suite)
	}

	spec := &aptRepositorySpec{
		Name:           name,
		URI:            strings.TrimSpace(plan.GetString("uri")),
		Suite:          suite,
		Components:     components,
		Architectures:  normalizedOptionalStringList(plan, "architectures"),
		SignedBy:       strings.TrimSpace(plan.GetString("signed_by")),
		SignedByKeyURL: signedByKeyURL(plan),
		State:          "present",
		FilePath:       path,
	}

	line, err := renderSourceLine(spec)
	if err != nil {
		return nil, err
	}
	spec.SourceLine = line

	return spec, nil
}

func readRepositoryAtPath(path string) (pluginsdk.StateData, error) {
	data, err := pluginsdk.FileRead(path)
	if err != nil {
		return nil, fmt.Errorf("read repository file %s: %w", path, err)
	}

	spec, err := parseRepositoryFile(string(data), path)
	if err != nil {
		return nil, err
	}

	state := specToState(spec)
	state["update_cache"] = false
	return state, nil
}

func adoptExistingRepository(plan pluginsdk.StateData, path string) (pluginsdk.StateData, error) {
	spec, err := desiredRepositorySpec(plan, path)
	if err != nil {
		return nil, err
	}

	existing, err := pluginsdk.FileRead(path)
	if err != nil {
		return nil, fmt.Errorf("read repository file %s: %w", path, err)
	}
	if string(existing) != renderRepositoryFile(spec) {
		return nil, nil
	}

	state := specToState(spec)
	state["update_cache"] = plan.GetBool("update_cache")
	return state, nil
}

func ensureRepositoryKeyring(plan pluginsdk.StateData) error {
	path := managedSignedByPath(plan)
	if path == "" {
		return nil
	}
	if err := aptkeyring.Install(signedByKeyURL(plan), path); err != nil {
		return fmt.Errorf("install apt keyring %s: %w", path, err)
	}
	return nil
}

func cleanupRepositoryKeyrings(prior, plan pluginsdk.StateData) error {
	paths := make([]string, 0, 2)
	if repositoryEnsure(plan) == "absent" {
		paths = append(paths, managedSignedByPath(prior), managedSignedByPath(plan))
	} else {
		oldPath := managedSignedByPath(prior)
		newPath := managedSignedByPath(plan)
		if oldPath != "" && oldPath != newPath {
			paths = append(paths, oldPath)
		}
	}

	seen := map[string]struct{}{}
	for _, candidate := range paths {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		if err := removeRepositoryKeyringIfUnused(candidate); err != nil {
			return err
		}
	}
	return nil
}

func removeRepositoryKeyringIfUnused(path string) error {
	referenced, err := aptkeyring.Referenced(path)
	if err != nil {
		return fmt.Errorf("check apt keyring references for %s: %w", path, err)
	}
	if referenced {
		return nil
	}
	if err := pluginsdk.FileDelete(path); err != nil {
		return fmt.Errorf("remove apt keyring %s: %w", path, err)
	}
	return nil
}

func writeRepositoryFile(path, content string) (bool, error) {
	existing, err := pluginsdk.FileRead(path)
	if err == nil && string(existing) == content {
		return false, nil
	}
	if err := pluginsdk.FileWrite(path, []byte(content), 0o644); err != nil {
		return false, fmt.Errorf("write repository file %s: %w", path, err)
	}
	return true, nil
}

func removeRepositoryFile(path string) (bool, error) {
	exists, err := pluginsdk.FileExists(path)
	if err != nil {
		return false, fmt.Errorf("check repository file %s: %w", path, err)
	}
	if !exists {
		return false, nil
	}
	if err := pluginsdk.FileDelete(path); err != nil {
		return false, fmt.Errorf("remove repository file %s: %w", path, err)
	}
	return true, nil
}

func aptUpdate() error {
	return pluginsdk.RetryCommand("apt-get update", "env", []string{"DEBIAN_FRONTEND=noninteractive", "apt-get", "update"}, pluginsdk.CommandRetryPolicy{
		MaxAttempts: aptRetryMaxAttempts,
		BaseDelay:   aptRetryBaseDelay,
		Sleep:       aptRetrySleep,
		IsTransient: isTransientAptBusy,
		OnRetry: func(delay time.Duration, detail string) {
			pluginsdk.LogInfo(fmt.Sprintf("apt-get update hit transient apt/dpkg contention; retrying in %s: %s", delay, detail))
		},
	})
}

func aptRetryBackoff(attempt int) time.Duration {
	return pluginsdk.ExponentialBackoff(aptRetryBaseDelay, attempt)
}

func isTransientAptBusy(detail string) bool {
	return pluginsdk.IsTransientAptBusy(detail)
}

func aptCommandFailureDetail(result *pluginsdk.CmdResult) string {
	return pluginsdk.CommandFailureDetail(result)
}

func ensureDebianAPT() error {
	profile, err := pluginsdk.GetHostProfile()
	if err != nil {
		return fmt.Errorf("detect host profile: %w", err)
	}
	if profile == nil {
		return fmt.Errorf("host profile unavailable")
	}
	if profile.DistroFamily != "debian" {
		return fmt.Errorf("apt_repository requires a Debian-family host, got distro family %q", profile.DistroFamily)
	}
	if profile.PackageManager != "apt" {
		return fmt.Errorf("apt_repository requires apt, got package manager %q", profile.PackageManager)
	}
	return nil
}

func defaultDebianSuite() (string, error) {
	data, err := pluginsdk.FileRead("/etc/os-release")
	if err != nil {
		return "", fmt.Errorf("read /etc/os-release: %w", err)
	}
	if suite := parseOSReleaseCodename(string(data)); suite != "" {
		return suite, nil
	}
	return "", fmt.Errorf("VERSION_CODENAME or UBUNTU_CODENAME not found in /etc/os-release")
}

func parseOSReleaseCodename(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, key := range []string{"VERSION_CODENAME", "UBUNTU_CODENAME"} {
			prefix := key + "="
			if strings.HasPrefix(line, prefix) {
				return strings.Trim(strings.TrimPrefix(line, prefix), `"'`)
			}
		}
	}
	return ""
}

func repositoryFilePath(data pluginsdk.StateData) (string, error) {
	if path := strings.TrimSpace(data.GetString("file_path")); path != "" {
		return path, nil
	}
	name := repositoryName(data)
	if name == "" {
		return "", fmt.Errorf("repository path requires name, file_path, or uri")
	}
	return filepath.Join(aptSourcesDir, name+".list"), nil
}

func repositoryName(data pluginsdk.StateData) string {
	if raw := strings.TrimSpace(data.GetString("name")); raw != "" {
		base := filepath.Base(raw)
		base = strings.TrimSuffix(base, ".list")
		return sanitizeRepoToken(base)
	}
	uri := strings.TrimSpace(data.GetString("uri"))
	if uri == "" {
		return ""
	}
	return sanitizeRepoToken(uri)
}

func sanitizeRepoToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"://", "-",
		"/", "-",
		":", "-",
		"?", "-",
		"&", "-",
		"=", "-",
		"@", "-",
		" ", "-",
		"_", "-",
		".", "-",
	)
	value = replacer.Replace(value)
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	if len(parts) == 0 {
		return "repo"
	}
	return strings.Join(parts, "-")
}

func renderRepositoryFile(spec *aptRepositorySpec) string {
	return fmt.Sprintf("# Managed by tf-linux-provider\n%s\n", spec.SourceLine)
}

func renderSourceLine(spec *aptRepositorySpec) (string, error) {
	if spec == nil {
		return "", fmt.Errorf("repository spec must not be nil")
	}
	if strings.TrimSpace(spec.URI) == "" {
		return "", fmt.Errorf("uri must not be empty")
	}
	if strings.TrimSpace(spec.Suite) == "" {
		return "", fmt.Errorf("suite must not be empty")
	}
	if !strings.HasSuffix(spec.Suite, "/") && len(spec.Components) == 0 {
		return "", fmt.Errorf("components must not be empty when suite %q is not an exact path", spec.Suite)
	}

	parts := []string{"deb"}
	options := []string{}
	if len(spec.Architectures) > 0 {
		options = append(options, "arch="+strings.Join(normalizeStrings(spec.Architectures), ","))
	}
	if spec.SignedBy != "" {
		options = append(options, "signed-by="+spec.SignedBy)
	}
	if len(options) > 0 {
		parts = append(parts, "["+strings.Join(options, " ")+"]")
	}
	parts = append(parts, spec.URI, spec.Suite)
	parts = append(parts, normalizeStrings(spec.Components)...)

	return strings.Join(parts, " "), nil
}

func parseRepositoryFile(content, path string) (*aptRepositorySpec, error) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return parseSourceLine(line, path)
	}
	return nil, fmt.Errorf("repository file %s does not contain a deb source line", path)
}

func parseSourceLine(line, path string) (*aptRepositorySpec, error) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "deb ") {
		return nil, fmt.Errorf("unsupported apt source line in %s: %q", path, line)
	}

	rest := strings.TrimSpace(strings.TrimPrefix(line, "deb"))
	spec := &aptRepositorySpec{
		Name:     strings.TrimSuffix(filepath.Base(path), ".list"),
		State:    "present",
		FilePath: path,
	}

	if strings.HasPrefix(rest, "[") {
		end := strings.Index(rest, "]")
		if end == -1 {
			return nil, fmt.Errorf("invalid apt source options in %s: %q", path, line)
		}
		options := strings.Fields(rest[1:end])
		for _, option := range options {
			key, value, ok := strings.Cut(option, "=")
			if !ok {
				continue
			}
			switch key {
			case "arch":
				spec.Architectures = normalizeStrings(strings.Split(value, ","))
			case "signed-by":
				spec.SignedBy = value
			}
		}
		rest = strings.TrimSpace(rest[end+1:])
	}

	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return nil, fmt.Errorf("invalid apt source line in %s: %q", path, line)
	}

	spec.URI = fields[0]
	spec.Suite = fields[1]
	spec.Components = normalizeStrings(fields[2:])
	if !strings.HasSuffix(spec.Suite, "/") && len(spec.Components) == 0 {
		return nil, fmt.Errorf("invalid apt source line in %s: suite %q requires at least one component", path, spec.Suite)
	}
	spec.SourceLine = line

	return spec, nil
}

func specToState(spec *aptRepositorySpec) pluginsdk.StateData {
	return pluginsdk.StateData{
		"id":                spec.FilePath,
		"name":              spec.Name,
		"uri":               spec.URI,
		"suite":             spec.Suite,
		"components":        stableStringList(spec.Components),
		"architectures":     spec.Architectures,
		"signed_by":         spec.SignedBy,
		"signed_by_key_url": nullableString(spec.SignedByKeyURL),
		"ensure":            spec.State,
		"file_path":         spec.FilePath,
		"source_line":       spec.SourceLine,
	}
}

func absentState(input pluginsdk.StateData, path string) pluginsdk.StateData {
	return pluginsdk.StateData{
		"id":                path,
		"name":              repositoryName(input),
		"uri":               strings.TrimSpace(input.GetString("uri")),
		"suite":             strings.TrimSpace(input.GetString("suite")),
		"components":        stableStringList(normalizeStrings(input.GetStringList("components"))),
		"architectures":     normalizedOptionalStringList(input, "architectures"),
		"signed_by":         strings.TrimSpace(input.GetString("signed_by")),
		"signed_by_key_url": nullableString(signedByKeyURL(input)),
		"ensure":            "absent",
		"update_cache":      input.GetBool("update_cache"),
		"file_path":         path,
	}
}

func normalizedOptionalStringList(data pluginsdk.StateData, key string) []string {
	raw, ok := data[key]
	if !ok {
		return nil
	}

	normalized := normalizeStrings(data.GetStringList(key))
	if normalized != nil {
		return normalized
	}

	switch typed := raw.(type) {
	case []string:
		if len(typed) == 0 {
			return []string{}
		}
	case []interface{}:
		if len(typed) == 0 {
			return []string{}
		}
	}

	return nil
}

func stableStringList(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return values
}

func normalizeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func withDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func repositoryEnsure(data pluginsdk.StateData) string {
	return withDefault(data.GetString("ensure"), "present")
}

func signedByKeyURL(data pluginsdk.StateData) string {
	return strings.TrimSpace(data.GetString("signed_by_key_url"))
}

func managedSignedByPath(data pluginsdk.StateData) string {
	if signedByKeyURL(data) == "" {
		return ""
	}
	path := strings.TrimSpace(data.GetString("signed_by"))
	if !filepath.IsAbs(path) {
		return ""
	}
	return path
}

func preservedSignedByKeyURL(prior, current pluginsdk.StateData) interface{} {
	priorPath := managedSignedByPath(prior)
	if priorPath == "" {
		return nil
	}
	if strings.TrimSpace(current.GetString("signed_by")) != priorPath {
		return nil
	}
	return nullableString(signedByKeyURL(prior))
}

func nullableString(value string) interface{} {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func init() {
	pluginsdk.RegisterResource(&aptRepositoryResource{})
}

func main() {
	pluginsdk.Run()
}
