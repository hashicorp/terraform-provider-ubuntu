// Copyright IBM Corp. 2026

package main

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/aptkeyring"
)

func TestRepositoryFilePath(t *testing.T) {
	t.Parallel()

	got, err := repositoryFilePath(pluginsdk.StateData{"uri": "https://download.docker.com/linux/ubuntu"})
	if err != nil {
		t.Fatalf("repositoryFilePath returned error: %v", err)
	}

	want := filepath.Join(aptSourcesDir, "https-download-docker-com-linux-ubuntu.list")
	if got != want {
		t.Fatalf("unexpected repository path: got %q want %q", got, want)
	}
}

func TestSpecToStatePreservesExactPathComponentsAndOmittedArchitectures(t *testing.T) {
	t.Parallel()

	state := specToState(&aptRepositorySpec{
		Name:     "kubernetes",
		URI:      "https://pkgs.k8s.io/core:/stable:/v1.34/deb/",
		Suite:    "/",
		State:    "present",
		FilePath: filepath.Join(aptSourcesDir, "kubernetes.list"),
	})

	components, ok := state["components"].([]string)
	if !ok {
		t.Fatalf("expected components to be []string, got %#v", state["components"])
	}
	if len(components) != 0 {
		t.Fatalf("expected empty components list, got %#v", components)
	}

	architectures, ok := state["architectures"].([]string)
	if !ok {
		t.Fatal("expected architectures key to be present")
	}
	if architectures != nil {
		t.Fatalf("expected omitted architectures to stay nil, got %#v", architectures)
	}
}

func TestNormalizedOptionalStringListPreservesOmissionAndExplicitEmpty(t *testing.T) {
	t.Parallel()

	if got := normalizedOptionalStringList(pluginsdk.StateData{}, "architectures"); got != nil {
		t.Fatalf("expected omitted list to stay nil, got %#v", got)
	}

	got := normalizedOptionalStringList(pluginsdk.StateData{
		"architectures": []string{},
	}, "architectures")
	if got == nil {
		t.Fatal("expected explicit empty list to stay non-nil")
	}
	if len(got) != 0 {
		t.Fatalf("expected explicit empty list, got %#v", got)
	}
}

func TestRenderAndParseSourceLine(t *testing.T) {
	t.Parallel()

	spec := &aptRepositorySpec{
		Name:          "docker",
		URI:           "https://download.docker.com/linux/ubuntu",
		Suite:         "jammy",
		Components:    []string{"stable"},
		Architectures: []string{"amd64", "arm64"},
		SignedBy:      "/usr/share/keyrings/docker.gpg",
		FilePath:      filepath.Join(aptSourcesDir, "docker.list"),
	}

	line, err := renderSourceLine(spec)
	if err != nil {
		t.Fatalf("renderSourceLine returned error: %v", err)
	}

	parsed, err := parseSourceLine(line, spec.FilePath)
	if err != nil {
		t.Fatalf("parseSourceLine returned error: %v", err)
	}

	if parsed.URI != spec.URI {
		t.Fatalf("unexpected uri: got %q want %q", parsed.URI, spec.URI)
	}
	if parsed.Suite != spec.Suite {
		t.Fatalf("unexpected suite: got %q want %q", parsed.Suite, spec.Suite)
	}
	if parsed.SignedBy != spec.SignedBy {
		t.Fatalf("unexpected signed_by: got %q want %q", parsed.SignedBy, spec.SignedBy)
	}
	if !reflect.DeepEqual(parsed.Components, spec.Components) {
		t.Fatalf("unexpected components: got %#v want %#v", parsed.Components, spec.Components)
	}
	if !reflect.DeepEqual(parsed.Architectures, spec.Architectures) {
		t.Fatalf("unexpected architectures: got %#v want %#v", parsed.Architectures, spec.Architectures)
	}
}

func TestParseOSReleaseCodename(t *testing.T) {
	t.Parallel()

	content := "NAME=Ubuntu\nVERSION_CODENAME=jammy\nUBUNTU_CODENAME=jammy\n"
	if got := parseOSReleaseCodename(content); got != "jammy" {
		t.Fatalf("unexpected codename: got %q want jammy", got)
	}
}

func TestRepositoryEnsureDefaultsToPresent(t *testing.T) {
	t.Parallel()

	if got := repositoryEnsure(nil); got != "present" {
		t.Fatalf("unexpected default ensure: got %q want present", got)
	}
	if got := repositoryEnsure(pluginsdk.StateData{"ensure": "absent"}); got != "absent" {
		t.Fatalf("unexpected explicit ensure: got %q want absent", got)
	}
}

func TestCreateExistingRepositoryRequiresImport(t *testing.T) {
	origGetHostProfile := pluginsdk.GetHostProfile
	origHasCommand := pluginsdk.HasCommand
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	t.Cleanup(func() {
		pluginsdk.GetHostProfile = origGetHostProfile
		pluginsdk.HasCommand = origHasCommand
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
	})

	pluginsdk.GetHostProfile = func() (*pluginsdk.HostProfile, error) {
		return &pluginsdk.HostProfile{DistroFamily: "debian", PackageManager: "apt"}, nil
	}
	pluginsdk.HasCommand = func(name string) (bool, error) { return name == "apt-get", nil }
	pluginsdk.FileExists = func(path string) (bool, error) {
		return path == filepath.Join(aptSourcesDir, "docker.list"), nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		return []byte("# unmanaged\n"), nil
	}

	_, err := (&aptRepositoryResource{}).Create(pluginsdk.StateData{
		"name":       "docker",
		"uri":        "https://download.docker.com/linux/ubuntu",
		"suite":      "jammy",
		"components": []string{"stable"},
	})
	if err == nil || !strings.Contains(err.Error(), "import it before managing with terraform") {
		t.Fatalf("expected import-required error, got %v", err)
	}
}

func TestCreateExistingRepositoryAdoptsMatchingManagedContent(t *testing.T) {
	origGetHostProfile := pluginsdk.GetHostProfile
	origHasCommand := pluginsdk.HasCommand
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	t.Cleanup(func() {
		pluginsdk.GetHostProfile = origGetHostProfile
		pluginsdk.HasCommand = origHasCommand
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
	})

	pluginsdk.GetHostProfile = func() (*pluginsdk.HostProfile, error) {
		return &pluginsdk.HostProfile{DistroFamily: "debian", PackageManager: "apt"}, nil
	}
	pluginsdk.HasCommand = func(name string) (bool, error) { return name == "apt-get", nil }
	pluginsdk.FileExists = func(path string) (bool, error) {
		return path == filepath.Join(aptSourcesDir, "kubernetes.list"), nil
	}

	spec := &aptRepositorySpec{
		Name:       "kubernetes",
		URI:        "https://pkgs.k8s.io/core:/stable:/v1.34/deb/",
		Suite:      "/",
		SignedBy:   "/etc/apt/keyrings/kubernetes-apt-keyring.gpg",
		State:      "present",
		FilePath:   filepath.Join(aptSourcesDir, "kubernetes.list"),
		SourceLine: "deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v1.34/deb/ /",
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		return []byte(renderRepositoryFile(spec)), nil
	}

	state, err := (&aptRepositoryResource{}).Create(pluginsdk.StateData{
		"name":          "kubernetes",
		"uri":           spec.URI,
		"suite":         "/",
		"components":    []string{},
		"architectures": []string{},
		"signed_by":     spec.SignedBy,
		"update_cache":  false,
	})
	if err != nil {
		t.Fatalf("expected matching managed repository to be adopted, got %v", err)
	}
	if got := state.GetString("id"); got != spec.FilePath {
		t.Fatalf("unexpected adopted id: got %q want %q", got, spec.FilePath)
	}
}

func TestCreateRepositoryPreservesEmptyListsInState(t *testing.T) {
	origGetHostProfile := pluginsdk.GetHostProfile
	origHasCommand := pluginsdk.HasCommand
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origFileWrite := pluginsdk.FileWrite
	t.Cleanup(func() {
		pluginsdk.GetHostProfile = origGetHostProfile
		pluginsdk.HasCommand = origHasCommand
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileWrite = origFileWrite
	})

	pluginsdk.GetHostProfile = func() (*pluginsdk.HostProfile, error) {
		return &pluginsdk.HostProfile{DistroFamily: "debian", PackageManager: "apt"}, nil
	}
	pluginsdk.HasCommand = func(name string) (bool, error) { return name == "apt-get", nil }
	pluginsdk.FileExists = func(path string) (bool, error) {
		return false, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		return nil, fmt.Errorf("not found")
	}
	pluginsdk.FileWrite = func(path string, content []byte, mode uint32) error {
		return nil
	}

	state, err := (&aptRepositoryResource{}).Create(pluginsdk.StateData{
		"name":          "kubernetes",
		"uri":           "https://pkgs.k8s.io/core:/stable:/v1.31/deb/",
		"suite":         "/",
		"components":    []string{},
		"architectures": []string{},
		"signed_by":     "/etc/apt/keyrings/kubernetes-apt-keyring.gpg",
		"update_cache":  false,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	components, ok := state["components"].([]string)
	if !ok {
		t.Fatalf("expected components to be []string, got %#v", state["components"])
	}
	if components == nil {
		t.Fatal("expected components slice to be non-nil")
	}
	if len(components) != 0 {
		t.Fatalf("expected empty components list, got %#v", components)
	}

	architectures, ok := state["architectures"].([]string)
	if !ok {
		t.Fatalf("expected architectures to be []string, got %#v", state["architectures"])
	}
	if architectures == nil {
		t.Fatal("expected architectures slice to be non-nil")
	}
	if len(architectures) != 0 {
		t.Fatalf("expected empty architectures list, got %#v", architectures)
	}
}

func TestCreateRepositoryPreservesOmittedArchitecturesAsNull(t *testing.T) {
	origGetHostProfile := pluginsdk.GetHostProfile
	origHasCommand := pluginsdk.HasCommand
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origFileWrite := pluginsdk.FileWrite
	t.Cleanup(func() {
		pluginsdk.GetHostProfile = origGetHostProfile
		pluginsdk.HasCommand = origHasCommand
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileWrite = origFileWrite
	})

	pluginsdk.GetHostProfile = func() (*pluginsdk.HostProfile, error) {
		return &pluginsdk.HostProfile{DistroFamily: "debian", PackageManager: "apt"}, nil
	}
	pluginsdk.HasCommand = func(name string) (bool, error) { return name == "apt-get", nil }
	pluginsdk.FileExists = func(path string) (bool, error) {
		return false, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		return nil, fmt.Errorf("not found")
	}
	pluginsdk.FileWrite = func(path string, content []byte, mode uint32) error {
		return nil
	}

	state, err := (&aptRepositoryResource{}).Create(pluginsdk.StateData{
		"name":         "kubernetes",
		"uri":          "https://pkgs.k8s.io/core:/stable:/v1.31/deb/",
		"suite":        "/",
		"components":   []string{},
		"signed_by":    "/etc/apt/keyrings/kubernetes-apt-keyring.gpg",
		"update_cache": false,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	architectures, ok := state["architectures"].([]string)
	if !ok {
		t.Fatal("expected architectures key to be present")
	}
	if architectures != nil {
		t.Fatalf("expected omitted architectures to stay nil, got %#v", architectures)
	}
}

func TestAptUpdateRetriesTransientContention(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origLogInfo := pluginsdk.LogInfo
	origAttempts := aptRetryMaxAttempts
	origDelay := aptRetryBaseDelay
	origSleep := aptRetrySleep
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.LogInfo = origLogInfo
		aptRetryMaxAttempts = origAttempts
		aptRetryBaseDelay = origDelay
		aptRetrySleep = origSleep
	})

	aptRetryMaxAttempts = 3
	aptRetryBaseDelay = time.Millisecond
	aptRetrySleep = func(time.Duration) {}
	pluginsdk.LogInfo = func(string) {}

	var calls int
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		calls++
		if calls < 3 {
			return &pluginsdk.CmdResult{
				ExitCode: 100,
				Stderr:   "E: Could not get lock /var/lib/apt/lists/lock. It is held by process 999",
			}, nil
		}
		return &pluginsdk.CmdResult{ExitCode: 0}, nil
	}

	if err := aptUpdate(); err != nil {
		t.Fatalf("expected transient apt contention to recover, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", calls)
	}
}

func TestAptUpdateFailsFastOnPermanentError(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	origLogInfo := pluginsdk.LogInfo
	origAttempts := aptRetryMaxAttempts
	origDelay := aptRetryBaseDelay
	origSleep := aptRetrySleep
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
		pluginsdk.LogInfo = origLogInfo
		aptRetryMaxAttempts = origAttempts
		aptRetryBaseDelay = origDelay
		aptRetrySleep = origSleep
	})

	aptRetryMaxAttempts = 3
	aptRetryBaseDelay = time.Millisecond
	aptRetrySleep = func(time.Duration) {}
	pluginsdk.LogInfo = func(string) {}

	var calls int
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		calls++
		return &pluginsdk.CmdResult{
			ExitCode: 100,
			Stderr:   "E: The repository 'https://example.invalid jammy Release' does not have a Release file.",
		}, nil
	}

	err := aptUpdate()
	if err == nil {
		t.Fatal("expected permanent apt-get update failure")
	}
	if !strings.Contains(err.Error(), "does not have a Release file") {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected permanent failure to avoid retry, got %d attempts", calls)
	}
}

func TestCreateRepositoryInstallsManagedKeyringThroughHost(t *testing.T) {
	origGetHostProfile := pluginsdk.GetHostProfile
	origHasCommand := pluginsdk.HasCommand
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origFileWrite := pluginsdk.FileWrite
	origFetchURL := pluginsdk.FetchURL
	origDirEnsure := pluginsdk.DirEnsure
	t.Cleanup(func() {
		pluginsdk.GetHostProfile = origGetHostProfile
		pluginsdk.HasCommand = origHasCommand
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.FetchURL = origFetchURL
		pluginsdk.DirEnsure = origDirEnsure
	})

	pluginsdk.GetHostProfile = func() (*pluginsdk.HostProfile, error) {
		return &pluginsdk.HostProfile{DistroFamily: "debian", PackageManager: "apt"}, nil
	}
	pluginsdk.HasCommand = func(name string) (bool, error) { return name == "apt-get", nil }
	pluginsdk.FileExists = func(string) (bool, error) { return false, nil }
	pluginsdk.FileRead = func(string) ([]byte, error) { return nil, fmt.Errorf("not found") }
	const expectedKeyringPath = "/etc/apt/keyrings/kubernetes-apt-keyring.gpg"
	var installedURL, installedPath string
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		if path == expectedKeyringPath {
			installedPath = path
		}
		return nil
	}
	pluginsdk.FetchURL = func(url string) ([]byte, error) {
		installedURL = url
		return []byte("raw-key"), nil
	}
	pluginsdk.DirEnsure = func(string, uint32) error { return nil }

	state, err := (&aptRepositoryResource{}).Create(pluginsdk.StateData{
		"name":              "kubernetes",
		"uri":               "https://pkgs.k8s.io/core:/stable:/v1.31/deb/",
		"suite":             "/",
		"components":        []string{},
		"signed_by":         "/etc/apt/keyrings/kubernetes-apt-keyring.gpg",
		"signed_by_key_url": "https://pkgs.k8s.io/core:/stable:/v1.31/deb/Release.key",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if installedURL != "https://pkgs.k8s.io/core:/stable:/v1.31/deb/Release.key" {
		t.Fatalf("unexpected installed key url: %q", installedURL)
	}
	if installedPath != expectedKeyringPath {
		t.Fatalf("unexpected installed key path: %q", installedPath)
	}
	if got := state.GetString("signed_by_key_url"); got != installedURL {
		t.Fatalf("unexpected state key url: %q", got)
	}
}

func TestDeleteRepositoryKeepsSharedManagedKeyring(t *testing.T) {
	origGetHostProfile := pluginsdk.GetHostProfile
	origHasCommand := pluginsdk.HasCommand
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origReadDir := pluginsdk.ReadDir
	origFileDelete := pluginsdk.FileDelete
	t.Cleanup(func() {
		pluginsdk.GetHostProfile = origGetHostProfile
		pluginsdk.HasCommand = origHasCommand
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.ReadDir = origReadDir
		pluginsdk.FileDelete = origFileDelete
	})

	pluginsdk.GetHostProfile = func() (*pluginsdk.HostProfile, error) {
		return &pluginsdk.HostProfile{DistroFamily: "debian", PackageManager: "apt"}, nil
	}
	pluginsdk.HasCommand = func(name string) (bool, error) { return name == "apt-get", nil }
	pluginsdk.FileExists = func(string) (bool, error) { return true, nil }
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		switch path {
		case aptkeyring.DefaultSourcesListPath:
			return nil, fmt.Errorf("open %s: no such file or directory", path)
		case filepath.Join(aptkeyring.DefaultSourcesListDirPath, "shared.list"):
			return []byte("deb [signed-by=/etc/apt/keyrings/shared.gpg] https://example.invalid stable main\n"), nil
		default:
			return nil, fmt.Errorf("unexpected file read path: %q", path)
		}
	}
	pluginsdk.ReadDir = func(path string) ([]pluginsdk.DirEntry, error) {
		if path != aptkeyring.DefaultSourcesListDirPath {
			return nil, fmt.Errorf("unexpected dir read path: %q", path)
		}
		return []pluginsdk.DirEntry{{Name: "shared.list"}}, nil
	}
	deletedPaths := make([]string, 0, 2)
	pluginsdk.FileDelete = func(path string) error {
		deletedPaths = append(deletedPaths, path)
		return nil
	}

	err := (&aptRepositoryResource{}).Delete(pluginsdk.StateData{
		"name":              "kubernetes",
		"uri":               "https://pkgs.k8s.io/core:/stable:/v1.31/deb/",
		"file_path":         filepath.Join(aptSourcesDir, "kubernetes.list"),
		"signed_by":         "/etc/apt/keyrings/shared.gpg",
		"signed_by_key_url": "https://pkgs.k8s.io/core:/stable:/v1.31/deb/Release.key",
	})
	if err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if len(deletedPaths) != 1 || deletedPaths[0] != filepath.Join(aptSourcesDir, "kubernetes.list") {
		t.Fatalf("unexpected deleted paths: %#v", deletedPaths)
	}
}
