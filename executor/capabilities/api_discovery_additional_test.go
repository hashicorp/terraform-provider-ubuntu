//go:build !js && !wasm

package capabilities

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestHostAPIFileLifecycleAndProfileHelpers(t *testing.T) {
	ctx := context.Background()
	profile := HostProfile{Hostname: "node-1", Extra: map[string]string{"role": "test"}}
	h := NewHostAPI(profile)
	if h == nil || h.locks == nil {
		t.Fatal("NewHostAPI() should initialize the lock map")
	}

	root := t.TempDir()
	filePath := filepath.Join(root, "config.txt")
	if err := h.FileWrite(ctx, filePath, "hello\n", 0o640); err != nil {
		t.Fatalf("FileWrite() error = %v, want nil", err)
	}

	content, err := h.FileRead(filePath)
	if err != nil || content != "hello\n" {
		t.Fatalf("FileRead() = (%q, %v), want (hello\\n, nil)", content, err)
	}
	content, err = h.FileReadWithContext(ctx, filePath)
	if err != nil || content != "hello\n" {
		t.Fatalf("FileReadWithContext() = (%q, %v), want (hello\\n, nil)", content, err)
	}
	if !h.FileExists(filePath) {
		t.Fatal("FileExists() should report the created file")
	}

	stat, err := h.FileStat(filePath)
	if err != nil {
		t.Fatalf("FileStat() error = %v, want nil", err)
	}
	if stat.Path != filePath || stat.Size != int64(len("hello\n")) || stat.Digest == "" {
		t.Fatalf("FileStat() = %#v, want populated file stat", stat)
	}
	statWithContext, err := h.FileStatWithContext(ctx, filePath)
	if err != nil {
		t.Fatalf("FileStatWithContext() error = %v, want nil", err)
	}
	if statWithContext.Digest != stat.Digest {
		t.Fatalf("FileStatWithContext() digest = %q, want %q", statWithContext.Digest, stat.Digest)
	}

	if err := h.FileChmod(ctx, filePath, 0o600); err != nil {
		t.Fatalf("FileChmod() error = %v, want nil", err)
	}
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat file after chmod: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %#o, want 0600", info.Mode().Perm())
	}

	nestedDir := filepath.Join(root, "nested")
	if err := h.DirEnsure(ctx, nestedDir, 0o755); err != nil {
		t.Fatalf("DirEnsure() error = %v, want nil", err)
	}
	entries, err := h.ReadDir(ctx, root)
	if err != nil {
		t.Fatalf("ReadDir() error = %v, want nil", err)
	}
	if !hasDirEntry(entries, "config.txt", false) || !hasDirEntry(entries, "nested", true) {
		t.Fatalf("ReadDir() = %#v, want config.txt and nested entries", entries)
	}

	renamedPath := filepath.Join(root, "renamed.txt")
	if err := h.FileRename(ctx, filePath, renamedPath); err != nil {
		t.Fatalf("FileRename() error = %v, want nil", err)
	}
	linkPath := filepath.Join(root, "config.link")
	if err := h.FileSymlink(ctx, renamedPath, linkPath); err != nil {
		t.Fatalf("FileSymlink() error = %v, want nil", err)
	}
	target, err := h.FileReadlink(ctx, linkPath)
	if err != nil {
		t.Fatalf("FileReadlink() error = %v, want nil", err)
	}
	if target != renamedPath {
		t.Fatalf("FileReadlink() = %q, want %q", target, renamedPath)
	}
	if err := h.FileDelete(ctx, linkPath); err != nil {
		t.Fatalf("FileDelete() error = %v, want nil", err)
	}
	if h.FileExists(linkPath) {
		t.Fatal("FileDelete() should remove the symlink path")
	}

	rawProfile, err := h.HostProfileJSON()
	if err != nil {
		t.Fatalf("HostProfileJSON() error = %v, want nil", err)
	}
	var decoded HostProfile
	if err := json.Unmarshal([]byte(rawProfile), &decoded); err != nil {
		t.Fatalf("unmarshal HostProfileJSON(): %v", err)
	}
	if decoded.Hostname != profile.Hostname || decoded.Extra["role"] != "test" {
		t.Fatalf("HostProfileJSON() = %#v, want hostname and extra values", decoded)
	}

	h.LogInfo("host api info log")
	h.LogWarn("host api warn log")
}

func TestHostAPILockCommandFetchIdentityAndOwnershipHelpers(t *testing.T) {
	ctx := context.Background()
	h := NewHostAPI(HostProfile{})
	root := t.TempDir()
	filePath := filepath.Join(root, "owned.txt")
	if err := os.WriteFile(filePath, []byte("data"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := h.FileLock(ctx, filePath); err != nil {
		t.Fatalf("FileLock() error = %v, want nil", err)
	}
	if err := h.FileLock(ctx, filePath); err == nil || !strings.Contains(err.Error(), "already locked") {
		t.Fatalf("FileLock() duplicate error = %v, want already locked", err)
	}
	if err := h.FileUnlock(filePath); err != nil {
		t.Fatalf("FileUnlock() error = %v, want nil", err)
	}
	if err := h.FileUnlock(filePath); err == nil || !strings.Contains(err.Error(), "not locked") {
		t.Fatalf("FileUnlock() second error = %v, want not locked", err)
	}

	binDir := filepath.Join(root, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	argsPath := filepath.Join(root, "chown.args")
	writeExecutable(t, filepath.Join(binDir, "customcmd"), "#!/bin/sh\nprintf 'custom:%s' \"$1\"\n")
	writeExecutable(t, filepath.Join(binDir, "chown"), fmt.Sprintf("#!/bin/sh\nprintf '%%s' \"$*\" > %q\n", argsPath))
	t.Setenv("PATH", binDir)

	result := h.CmdExec(ctx, "customcmd", "hello")
	if result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != "custom:hello" {
		t.Fatalf("CmdExec() = %#v, want exit 0 and custom output", result)
	}
	if !h.CmdExists("customcmd") {
		t.Fatal("CmdExists() should find customcmd in PATH")
	}
	if h.CmdExists("missing-command") {
		t.Fatal("CmdExists() should report false for a missing command")
	}

	if err := h.FileChownNames(ctx, filePath, "missing-owner", "missing-group"); err != nil {
		t.Fatalf("FileChownNames() fallback error = %v, want nil", err)
	}
	rawArgs, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read chown args: %v", err)
	}
	if got, want := strings.TrimSpace(string(rawArgs)), "missing-owner:missing-group "+filePath; got != want {
		t.Fatalf("FileChownNames() fallback args = %q, want %q", got, want)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("downloaded\n"))
	}))
	defer server.Close()
	body, err := h.FetchURL(ctx, server.URL)
	if err != nil || string(body) != "downloaded\n" {
		t.Fatalf("FetchURL() = (%q, %v), want (downloaded\\n, nil)", string(body), err)
	}

	passwdEntries, err := readPasswdEntries("/etc/passwd")
	if err != nil || len(passwdEntries) == 0 {
		t.Fatalf("readPasswdEntries(/etc/passwd) = (%#v, %v), want at least one entry", passwdEntries, err)
	}
	groupEntries, err := readGroupEntries("/etc/group")
	if err != nil || len(groupEntries) == 0 {
		t.Fatalf("readGroupEntries(/etc/group) = (%#v, %v), want at least one entry", groupEntries, err)
	}
	systemUser := passwdEntries[0]
	for _, entry := range passwdEntries {
		if entry.UID >= 0 && entry.GID >= 0 {
			systemUser = entry
			break
		}
	}
	systemGroup := groupEntries[0]
	for _, entry := range groupEntries {
		if entry.GID == systemUser.GID {
			systemGroup = entry
			break
		}
	}

	identityUser, err := h.LookupUser(systemUser.Name)
	if err != nil || identityUser == nil || identityUser.Name != systemUser.Name {
		t.Fatalf("LookupUser() = (%#v, %v), want %q", identityUser, err, systemUser.Name)
	}
	identityGroup, err := h.LookupGroup(systemGroup.Name)
	if err != nil || identityGroup == nil || identityGroup.Name != systemGroup.Name {
		t.Fatalf("LookupGroup() = (%#v, %v), want %q", identityGroup, err, systemGroup.Name)
	}

	uid, gid, err := LookupOwnership(systemUser.Name, systemGroup.Name)
	if err != nil {
		t.Fatalf("LookupOwnership(names) error = %v", err)
	}
	wantUID := systemUser.UID
	wantGID := systemGroup.GID
	if uid != wantUID || gid != wantGID {
		t.Fatalf("LookupOwnership(names) = (%d, %d), want (%d, %d)", uid, gid, wantUID, wantGID)
	}
	uid, gid, err = LookupOwnership(fmt.Sprintf("%d", systemUser.UID), fmt.Sprintf("%d", systemGroup.GID))
	if err != nil || uid != wantUID || gid != wantGID {
		t.Fatalf("LookupOwnership(numeric) = (%d, %d, %v), want (%d, %d, nil)", uid, gid, err, wantUID, wantGID)
	}
	uid, gid, err = LookupOwnership("", "")
	if err != nil || uid != 0 || gid != 0 {
		t.Fatalf("LookupOwnership(empty) = (%d, %d, %v), want (0, 0, nil)", uid, gid, err)
	}

	lockPath := lockFilePath(filePath)
	if !strings.Contains(lockPath, fileLockDirName) || !strings.HasSuffix(lockPath, ".lock") {
		t.Fatalf("lockFilePath() = %q, want lock dir path ending in .lock", lockPath)
	}
	if got := commandErrorText(CmdResult{Stdout: "stdout", Stderr: "stderr"}); got != "stderr" {
		t.Fatalf("commandErrorText(stderr) = %q, want stderr", got)
	}
	if got := commandErrorText(CmdResult{Stdout: "stdout"}); got != "stdout" {
		t.Fatalf("commandErrorText(stdout) = %q, want stdout", got)
	}
	if !shouldFallbackToOverwrite(syscall.EBUSY) || !shouldFallbackToOverwrite(syscall.EXDEV) || !shouldFallbackToOverwrite(syscall.EPERM) {
		t.Fatal("shouldFallbackToOverwrite() should recognize retryable rename errors")
	}
	if shouldFallbackToOverwrite(fmt.Errorf("boom")) {
		t.Fatal("shouldFallbackToOverwrite() should ignore unrelated errors")
	}
}

func TestHostFileHelperWritersAndIdentityReaders(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "status.txt")
	if err := os.WriteFile(filePath, []byte("payload\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	linkPath := filepath.Join(root, "status.link")
	if err := os.Symlink(filePath, linkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	digest, err := fileDigest(filePath)
	if err != nil || strings.TrimSpace(digest) == "" {
		t.Fatalf("fileDigest() = (%q, %v), want non-empty digest", digest, err)
	}

	var statOutput bytes.Buffer
	if err := WriteFileStat(filePath, &statOutput); err != nil {
		t.Fatalf("WriteFileStat() error = %v", err)
	}
	var stat FileStat
	if err := json.Unmarshal(statOutput.Bytes(), &stat); err != nil {
		t.Fatalf("unmarshal WriteFileStat() output: %v", err)
	}
	if stat.Path != filePath || stat.Digest != digest {
		t.Fatalf("WriteFileStat() = %#v, want path %q and digest %q", stat, filePath, digest)
	}

	var dirOutput bytes.Buffer
	if err := WriteDirEntries(root, &dirOutput); err != nil {
		t.Fatalf("WriteDirEntries() error = %v", err)
	}
	var entries []DirEntry
	if err := json.Unmarshal(dirOutput.Bytes(), &entries); err != nil {
		t.Fatalf("unmarshal WriteDirEntries() output: %v", err)
	}
	if !hasDirEntry(entries, "status.txt", false) || !hasDirEntry(entries, "status.link", false) {
		t.Fatalf("WriteDirEntries() = %#v, want file and link entries", entries)
	}

	var linkOutput bytes.Buffer
	if err := WriteReadlinkTarget(linkPath, &linkOutput); err != nil {
		t.Fatalf("WriteReadlinkTarget() error = %v", err)
	}
	if linkOutput.String() != filePath {
		t.Fatalf("WriteReadlinkTarget() = %q, want %q", linkOutput.String(), filePath)
	}

	passwdPath := filepath.Join(root, "passwd")
	groupPath := filepath.Join(root, "group")
	if err := os.WriteFile(passwdPath, []byte("alice:x:1000:1000:Alice Example:/home/alice:/bin/bash\n"), 0o644); err != nil {
		t.Fatalf("write passwd fixture: %v", err)
	}
	if err := os.WriteFile(groupPath, []byte("developers:x:1000:alice\n"), 0o644); err != nil {
		t.Fatalf("write group fixture: %v", err)
	}
	passwdEntries, err := readPasswdEntries(passwdPath)
	if err != nil || len(passwdEntries) != 1 || passwdEntries[0].Name != "alice" {
		t.Fatalf("readPasswdEntries() = (%#v, %v), want alice entry", passwdEntries, err)
	}
	groupEntries, err := readGroupEntries(groupPath)
	if err != nil || len(groupEntries) != 1 || groupEntries[0].Name != "developers" {
		t.Fatalf("readGroupEntries() = (%#v, %v), want developers entry", groupEntries, err)
	}
	resolvedGroup := resolveIdentityGroup("developers", groupEntries)
	if resolvedGroup == nil || resolvedGroup.GID != 1000 {
		t.Fatalf("resolveIdentityGroup() = %#v, want developers gid 1000", resolvedGroup)
	}
}

func TestDiscoveryHelpers(t *testing.T) {
	fields := parseOSRelease("# comment\nID=\"ubuntu\"\nNAME='Ubuntu'\nVERSION_ID=24.04\n")
	if fields["ID"] != "ubuntu" || fields["NAME"] != "Ubuntu" || fields["VERSION_ID"] != "24.04" {
		t.Fatalf("parseOSRelease() = %#v, want parsed os-release fields", fields)
	}

	if got := resolveDistroFamily("ubuntu"); got != "debian" {
		t.Fatalf("resolveDistroFamily(ubuntu) = %q, want debian", got)
	}
	if got := resolveDistroFamily("fedora"); got != "rhel" {
		t.Fatalf("resolveDistroFamily(fedora) = %q, want rhel", got)
	}
	if got := resolveDistroFamily("arch"); got != "arch" {
		t.Fatalf("resolveDistroFamily(arch) = %q, want arch", got)
	}
	if got := resolveDistroFamily("opensuse-leap"); got != "suse" {
		t.Fatalf("resolveDistroFamily(opensuse-leap) = %q, want suse", got)
	}
	if got := resolveDistroFamily("alpine"); got != "alpine" {
		t.Fatalf("resolveDistroFamily(alpine) = %q, want alpine", got)
	}
	if got := resolveDistroFamily("gentoo"); got != "gentoo" {
		t.Fatalf("resolveDistroFamily(gentoo) = %q, want gentoo", got)
	}
	if got := resolveDistroFamily("void"); got != "void" {
		t.Fatalf("resolveDistroFamily(void) = %q, want void", got)
	}
	if got := resolveDistroFamily("mystery"); got != "unknown" {
		t.Fatalf("resolveDistroFamily(mystery) = %q, want unknown", got)
	}

	if got := discoverHostname(); strings.TrimSpace(got) == "" {
		t.Fatal("discoverHostname() should return a non-empty hostname")
	}
	profile := Discover()
	if profile.Extra == nil || strings.TrimSpace(profile.Hostname) == "" || strings.TrimSpace(profile.Arch) == "" {
		t.Fatalf("Discover() = %#v, want hostname, arch, and extra map", profile)
	}
	if runtime.GOOS != "linux" && profile.Kernel != runtime.GOOS {
		t.Fatalf("Discover() kernel = %q, want %q on non-linux builds", profile.Kernel, runtime.GOOS)
	}

	manualProfile := HostProfile{}
	discoverKernel(&manualProfile)
	if strings.TrimSpace(manualProfile.Arch) == "" || strings.TrimSpace(manualProfile.Kernel) == "" {
		t.Fatalf("discoverKernel() = %#v, want arch and kernel", manualProfile)
	}
	if strings.TrimSpace(discoverInitSystem()) == "" {
		t.Fatal("discoverInitSystem() should always return a non-empty result")
	}
	_ = detectSELinux()
	_ = detectAppArmor()

	pathRoot := t.TempDir()
	binA := filepath.Join(pathRoot, "a")
	binB := filepath.Join(pathRoot, "b")
	if err := os.Mkdir(binA, 0o755); err != nil {
		t.Fatalf("mkdir binA: %v", err)
	}
	if err := os.Mkdir(binB, 0o755); err != nil {
		t.Fatalf("mkdir binB: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binA, "alpha"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write alpha: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binA, "dup"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write dup in binA: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binB, "beta"), []byte("b"), 0o644); err != nil {
		t.Fatalf("write beta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binB, "dup"), []byte("b"), 0o644); err != nil {
		t.Fatalf("write dup in binB: %v", err)
	}
	t.Setenv("PATH", strings.Join([]string{binA, binB}, string(os.PathListSeparator)))
	cmds := discoverCommands()
	if !containsString(cmds, "alpha") || !containsString(cmds, "beta") {
		t.Fatalf("discoverCommands() = %#v, want alpha and beta", cmds)
	}
	if countString(cmds, "dup") != 1 {
		t.Fatalf("discoverCommands() = %#v, want dup only once", cmds)
	}

	writeExecutable(t, filepath.Join(binA, "apt"), "#!/bin/sh\nexit 0\n")
	if got := detectPackageManager("unknown"); got != "apt" {
		t.Fatalf("detectPackageManager(unknown) = %q, want apt from PATH fallback", got)
	}
	t.Setenv("PATH", pathRoot)
	if got := detectPackageManager("unknown"); got != "unknown" {
		t.Fatalf("detectPackageManager(unknown) without managers = %q, want unknown", got)
	}
	if got := runtime.GOOS; got != "linux" {
		if manualProfile.Kernel != got {
			t.Fatalf("discoverKernel() kernel = %q, want %q on non-linux builds", manualProfile.Kernel, got)
		}
	}
}

func writeExecutable(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}

func hasDirEntry(entries []DirEntry, name string, isDir bool) bool {
	for _, entry := range entries {
		if entry.Name == name && entry.IsDir == isDir {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func countString(values []string, want string) int {
	count := 0
	for _, value := range values {
		if value == want {
			count++
		}
	}
	return count
}
