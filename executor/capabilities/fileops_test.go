//go:build !js && !wasm

package capabilities

import (
	"bytes"
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

func TestWriteFileContentsStreamsBytes(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "source.bin")
	expected := []byte{0x00, 0x01, 0x7f, 'a', '\n'}
	if err := os.WriteFile(path, expected, 0o600); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	var output bytes.Buffer
	if err := WriteFileContents(path, &output); err != nil {
		t.Fatalf("WriteFileContents returned error: %v", err)
	}
	if !bytes.Equal(output.Bytes(), expected) {
		t.Fatalf("unexpected streamed bytes: got %v want %v", output.Bytes(), expected)
	}
}

func TestWriteFileFromReaderCreatesFileWithMode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "written.conf")
	if err := WriteFileFromReader(path, strings.NewReader("content\n"), 0o640); err != nil {
		t.Fatalf("WriteFileFromReader returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(data) != "content\n" {
		t.Fatalf("unexpected file content: %q", string(data))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat written file: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("unexpected mode: %#o", info.Mode().Perm())
	}
}

func TestDirEnsureCreatesDirectoryWithMode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "apt", "keyrings")
	if err := DirEnsure(path, 0o755); err != nil {
		t.Fatalf("DirEnsure returned error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat ensured directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory, got mode %#o", info.Mode())
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("unexpected mode: %#o", info.Mode().Perm())
	}
}

func TestReadDirReturnsEntries(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "repo.list"), []byte("deb\n"), 0o644); err != nil {
		t.Fatalf("write repo file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	entries, err := ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir returned error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("unexpected entry count: %d", len(entries))
	}
	if entries[0].Name != "nested" || !entries[0].IsDir {
		t.Fatalf("unexpected first entry: %#v", entries[0])
	}
	if entries[1].Name != "repo.list" || entries[1].IsDir {
		t.Fatalf("unexpected second entry: %#v", entries[1])
	}
}

func TestDecodePrivilegedDirEntries(t *testing.T) {
	t.Parallel()

	entries, err := decodePrivilegedDirEntries("/etc/apt/sources.list.d", `[{"name":"kubernetes.list","is_dir":false},{"name":"nested","is_dir":true}]`)
	if err != nil {
		t.Fatalf("decodePrivilegedDirEntries returned error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("unexpected entry count: %d", len(entries))
	}
	if entries[0].Name != "kubernetes.list" || entries[0].IsDir {
		t.Fatalf("unexpected first entry: %#v", entries[0])
	}
	if entries[1].Name != "nested" || !entries[1].IsDir {
		t.Fatalf("unexpected second entry: %#v", entries[1])
	}
}

func TestFileDeleteRemovesExistingFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "stale.list")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := FileDelete(path); err != nil {
		t.Fatalf("FileDelete returned error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file to be deleted, stat err=%v", err)
	}
}

func TestFileDeleteIgnoresMissingFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing.list")
	if err := FileDelete(path); err != nil {
		t.Fatalf("FileDelete returned error for missing file: %v", err)
	}
}

func TestFileRenameMovesExistingFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	from := filepath.Join(root, "source.txt")
	to := filepath.Join(root, "dest.txt")
	if err := os.WriteFile(from, []byte("content"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	if err := FileRename(from, to); err != nil {
		t.Fatalf("FileRename returned error: %v", err)
	}
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Fatalf("expected source to be removed, stat err=%v", err)
	}
	data, err := os.ReadFile(to)
	if err != nil {
		t.Fatalf("read renamed file: %v", err)
	}
	if string(data) != "content" {
		t.Fatalf("unexpected renamed content: %q", string(data))
	}
}

func TestFileReadlinkReturnsTarget(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	path := filepath.Join(root, "link.txt")
	if err := os.WriteFile(target, []byte("content"), 0o644); err != nil {
		t.Fatalf("write target file: %v", err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	got, err := FileReadlink(path)
	if err != nil {
		t.Fatalf("FileReadlink returned error: %v", err)
	}
	if got != target {
		t.Fatalf("unexpected symlink target: got %q want %q", got, target)
	}
}

func TestFileSymlinkReplacesExistingFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	path := filepath.Join(root, "link.txt")
	if err := os.WriteFile(target, []byte("content"), 0o644); err != nil {
		t.Fatalf("write target file: %v", err)
	}
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}

	if err := FileSymlink(target, path); err != nil {
		t.Fatalf("FileSymlink returned error: %v", err)
	}
	got, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("read created symlink: %v", err)
	}
	if got != target {
		t.Fatalf("unexpected symlink target: got %q want %q", got, target)
	}
}

func TestDecodePrivilegedFileStatFile(t *testing.T) {
	t.Parallel()

	stat, err := decodePrivilegedFileStat("/etc/example.conf", `{"path":"/etc/example.conf","size":12,"mode":416,"uid":0,"gid":0,"owner":"root","group":"root","mod_time":1700000000,"is_dir":false,"digest":"blake3:deadbeef"}`)
	if err != nil {
		t.Fatalf("decode privileged file stat: %v", err)
	}
	if stat.Path != "/etc/example.conf" {
		t.Fatalf("unexpected path: %q", stat.Path)
	}
	if stat.Size != 12 {
		t.Fatalf("unexpected size: %d", stat.Size)
	}
	if stat.Mode != uint32(os.FileMode(0o640)) {
		t.Fatalf("unexpected mode: %#o", stat.Mode)
	}
	if stat.UID != 0 || stat.GID != 0 {
		t.Fatalf("unexpected ownership: uid=%d gid=%d", stat.UID, stat.GID)
	}
	if stat.Owner != "root" || stat.Group != "root" {
		t.Fatalf("unexpected owner/group: %q:%q", stat.Owner, stat.Group)
	}
	if stat.ModTime != 1700000000 {
		t.Fatalf("unexpected mod time: %d", stat.ModTime)
	}
	if stat.IsDir {
		t.Fatal("expected regular file")
	}
	if stat.Digest != "blake3:deadbeef" {
		t.Fatalf("unexpected digest: %q", stat.Digest)
	}
}

func TestDecodePrivilegedFileStatDirectory(t *testing.T) {
	t.Parallel()

	stat, err := decodePrivilegedFileStat("/etc/kubernetes", `{"path":"/etc/kubernetes","size":4096,"mode":2147484141,"uid":0,"gid":0,"owner":"root","group":"root","mod_time":1700000001,"is_dir":true,"digest":""}`)
	if err != nil {
		t.Fatalf("decode privileged directory stat: %v", err)
	}
	if !stat.IsDir {
		t.Fatal("expected directory")
	}
	if os.FileMode(stat.Mode)&os.ModeDir == 0 {
		t.Fatalf("expected directory mode bit, got %#o", stat.Mode)
	}
	if stat.Digest != "" {
		t.Fatalf("expected empty digest for directory, got %q", stat.Digest)
	}
}

func TestOverwriteFileReplacesContentAndMode(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	source := filepath.Join(root, "source.txt")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatalf("write target file: %v", err)
	}
	if err := os.WriteFile(source, []byte("new-content"), 0o600); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	if err := overwriteFile(target, source, 0o600); err != nil {
		t.Fatalf("overwriteFile returned error: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read overwritten file: %v", err)
	}
	if string(data) != "new-content" {
		t.Fatalf("unexpected overwritten content: %q", string(data))
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat overwritten file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("unexpected overwritten mode: %#o", info.Mode().Perm())
	}
}

func TestFileChownHelpersAndExecutionContext(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "owned.txt")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	uid := os.Getuid()
	gid := os.Getgid()
	if err := FileChown(path, uid, gid); err != nil {
		t.Fatalf("FileChown returned error: %v", err)
	}
	if err := FileChownWithContext(context.Background(), path, uid, gid); err != nil {
		t.Fatalf("FileChownWithContext returned error: %v", err)
	}

	h := NewHostAPI(HostProfile{})
	if err := h.FileChown(context.Background(), path, uid, gid); err != nil {
		t.Fatalf("HostAPI.FileChown returned error: %v", err)
	}

	if filesystemNeedsElevatedExecution(context.Background()) {
		t.Fatal("filesystemNeedsElevatedExecution should be false without execution context")
	}
	rootCtx := WithExecutionContext(context.Background(), &hostrpc.ExecutionContext{Become: true})
	if got, want := filesystemNeedsElevatedExecution(rootCtx), os.Geteuid() != 0; got != want {
		t.Fatalf("filesystemNeedsElevatedExecution(root) = %t, want %t", got, want)
	}
	currentUser, err := user.Current()
	if err == nil {
		userCtx := WithExecutionContext(context.Background(), &hostrpc.ExecutionContext{Become: true, BecomeUser: currentUser.Username})
		if filesystemNeedsElevatedExecution(userCtx) {
			t.Fatal("filesystemNeedsElevatedExecution should be false when targeting the current user")
		}
	}
}

func TestFileLockUsesSharedPermissions(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "locked.txt")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	lockPath := lockFilePath(path)
	lockDir := filepath.Dir(lockPath)
	t.Cleanup(func() {
		_ = os.Remove(lockPath)
	})

	lockFile, err := FileLock(path)
	if err != nil {
		t.Fatalf("FileLock returned error: %v", err)
	}
	defer func() {
		if err := FileUnlock(lockFile); err != nil {
			t.Fatalf("FileUnlock returned error: %v", err)
		}
	}()

	lockInfo, err := os.Stat(lockPath)
	if err != nil {
		t.Fatalf("stat lock file: %v", err)
	}
	if got := lockInfo.Mode().Perm(); got != fileLockFileMode {
		t.Fatalf("lock file mode = %#o, want %#o", got, fileLockFileMode)
	}

	dirInfo, err := os.Stat(lockDir)
	if err != nil {
		t.Fatalf("stat lock dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != fileLockDirMode.Perm() {
		t.Fatalf("lock dir mode = %#o, want %#o", got, fileLockDirMode.Perm())
	}
	if dirInfo.Mode()&os.ModeSticky == 0 {
		t.Fatal("lock dir should set the sticky bit for shared tempdir safety")
	}
}
