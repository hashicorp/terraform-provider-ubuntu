//go:build !js && !wasm

package capabilities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"

	digestutil "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/digest"
)

const fileLockDirName = "tf-nix-locks"

const (
	fileLockDirMode  = os.ModeSticky | 0o777
	fileLockFileMode = 0o666
)

type DirEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir,omitempty"`
}

// FileRead reads the entire contents of a file.
func FileRead(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(data), nil
}

func FileReadWithContext(ctx context.Context, path string) (string, error) {
	if !filesystemNeedsElevatedExecution(ctx) {
		return FileRead(path)
	}

	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executor path for read %s: %w", path, err)
	}

	result := CmdExec(ctx, executable, "--run-file-read", path)
	if result.ExitCode != 0 {
		return "", fmt.Errorf("read %s: %s", path, commandErrorText(result))
	}
	return result.Stdout, nil
}

// FileWrite atomically writes content to a file. It writes to a temp file
// in the same directory, sets permissions, then renames over the target.
func FileWrite(path string, content string, mode os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	tmp, err := os.CreateTemp(dir, "."+base+".tmp.*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	tmpPath := tmp.Name()

	// Cleanup on failure.
	success := false
	defer func() {
		if !success {
			tmp.Close()
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.WriteString(content); err != nil {
		return fmt.Errorf("write temp %s: %w", tmpPath, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("chmod temp %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temp %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		if !shouldFallbackToOverwrite(err) {
			return fmt.Errorf("rename %s -> %s: %w", tmpPath, path, err)
		}

		if err := overwriteFile(path, tmpPath, mode); err != nil {
			return fmt.Errorf("overwrite %s from %s: %w", path, tmpPath, err)
		}

		if err := os.Remove(tmpPath); err != nil {
			return fmt.Errorf("remove temp %s: %w", tmpPath, err)
		}
	}
	success = true
	return nil
}

func FileWriteWithContext(ctx context.Context, path string, content string, mode os.FileMode) error {
	if !filesystemNeedsElevatedExecution(ctx) {
		return FileWrite(path, content, mode)
	}

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executor path for write %s: %w", path, err)
	}

	modeStr := fmt.Sprintf("%04o", mode.Perm())
	result := cmdExecInput(ctx, strings.NewReader(content), executable, "--run-file-write", path, "--run-file-write-mode", modeStr)
	if result.ExitCode != 0 {
		return fmt.Errorf("write %s via privileged overwrite: %s", path, commandErrorText(result))
	}

	return nil
}

func DirEnsure(path string, mode os.FileMode) error {
	path = strings.TrimSpace(path)
	if path == "" || path == "." || path == "/" {
		return nil
	}

	if err := os.MkdirAll(path, mode); err != nil {
		return fmt.Errorf("create directory %s: %w", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("chmod directory %s: %w", path, err)
	}
	return nil
}

func DirEnsureWithContext(ctx context.Context, path string, mode os.FileMode) error {
	path = strings.TrimSpace(path)
	if path == "" || path == "." || path == "/" {
		return nil
	}

	if !filesystemNeedsElevatedExecution(ctx) {
		return DirEnsure(path, mode)
	}

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executor path for directory %s: %w", path, err)
	}

	modeStr := fmt.Sprintf("%04o", mode.Perm())
	result := CmdExec(ctx, executable, "--run-dir-ensure", path, "--run-dir-ensure-mode", modeStr)
	if result.ExitCode != 0 {
		return fmt.Errorf("create directory %s: %s", path, commandErrorText(result))
	}

	return nil
}

func ReadDir(path string) ([]DirEntry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("read directory %s: %w", path, err)
	}

	result := make([]DirEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, DirEntry{Name: entry.Name(), IsDir: entry.IsDir()})
	}
	return result, nil
}

func ReadDirWithContext(ctx context.Context, path string) ([]DirEntry, error) {
	if !filesystemNeedsElevatedExecution(ctx) {
		return ReadDir(path)
	}

	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executor path for dir read %s: %w", path, err)
	}

	result := CmdExec(ctx, executable, "--run-dir-read", path)
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("read directory %s: %s", path, commandErrorText(result))
	}

	return decodePrivilegedDirEntries(path, result.Stdout)
}

func FileDelete(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("remove %s: %w", path, err)
}

func FileDeleteWithContext(ctx context.Context, path string) error {
	if !filesystemNeedsElevatedExecution(ctx) {
		return FileDelete(path)
	}

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executor path for delete %s: %w", path, err)
	}

	result := CmdExec(ctx, executable, "--run-file-delete", path)
	if result.ExitCode != 0 {
		return fmt.Errorf("delete %s: %s", path, commandErrorText(result))
	}

	return nil
}

func FileRename(from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", from, to, err)
	}
	return nil
}

func FileRenameWithContext(ctx context.Context, from, to string) error {
	if !filesystemNeedsElevatedExecution(ctx) {
		return FileRename(from, to)
	}

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executor path for rename %s -> %s: %w", from, to, err)
	}

	result := CmdExec(ctx, executable, "--run-file-rename", from, "--run-file-rename-to", to)
	if result.ExitCode != 0 {
		return fmt.Errorf("rename %s -> %s: %s", from, to, commandErrorText(result))
	}

	return nil
}

func FileReadlink(path string) (string, error) {
	target, err := os.Readlink(path)
	if err != nil {
		return "", fmt.Errorf("readlink %s: %w", path, err)
	}
	return target, nil
}

func FileReadlinkWithContext(ctx context.Context, path string) (string, error) {
	if !filesystemNeedsElevatedExecution(ctx) {
		return FileReadlink(path)
	}

	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executor path for readlink %s: %w", path, err)
	}

	result := CmdExec(ctx, executable, "--run-file-readlink", path)
	if result.ExitCode != 0 {
		return "", fmt.Errorf("readlink %s: %s", path, commandErrorText(result))
	}

	return result.Stdout, nil
}

func FileSymlink(target, path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("symlink path %s is a directory", path)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove existing %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("lstat %s: %w", path, err)
	}

	if err := os.Symlink(target, path); err != nil {
		return fmt.Errorf("symlink %s -> %s: %w", path, target, err)
	}
	return nil
}

func FileSymlinkWithContext(ctx context.Context, target, path string) error {
	if !filesystemNeedsElevatedExecution(ctx) {
		return FileSymlink(target, path)
	}

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executor path for symlink %s -> %s: %w", path, target, err)
	}

	result := CmdExec(ctx, executable, "--run-file-symlink", path, "--run-file-symlink-target", target)
	if result.ExitCode != 0 {
		return fmt.Errorf("symlink %s -> %s: %s", path, target, commandErrorText(result))
	}

	return nil
}

func shouldFallbackToOverwrite(err error) bool {
	return errors.Is(err, syscall.EBUSY) || errors.Is(err, syscall.EXDEV) || errors.Is(err, syscall.EPERM)
}

func overwriteFile(path, tmpPath string, mode os.FileMode) error {
	src, err := os.Open(tmpPath)
	if err != nil {
		return fmt.Errorf("open temp %s: %w", tmpPath, err)
	}
	defer src.Close()

	dst, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("open target %s: %w", path, err)
	}

	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return fmt.Errorf("copy temp %s to %s: %w", tmpPath, path, err)
	}

	if err := dst.Chmod(mode); err != nil {
		dst.Close()
		return fmt.Errorf("chmod target %s: %w", path, err)
	}

	if err := dst.Sync(); err != nil {
		dst.Close()
		return fmt.Errorf("sync target %s: %w", path, err)
	}

	if err := dst.Close(); err != nil {
		return fmt.Errorf("close target %s: %w", path, err)
	}

	return nil
}

// FileExists returns true if the path exists.
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// FileStatInfo returns metadata for a file, including its digest.
func FileStatInfo(path string) (*FileStat, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}

	stat := info.Sys().(*syscall.Stat_t)

	fs := &FileStat{
		Path:    path,
		Size:    info.Size(),
		Mode:    uint32(info.Mode()),
		UID:     stat.Uid,
		GID:     stat.Gid,
		ModTime: info.ModTime().Unix(),
		IsDir:   info.IsDir(),
	}
	if owner, err := user.LookupId(fmt.Sprintf("%d", stat.Uid)); err == nil {
		fs.Owner = owner.Username
	}
	if group, err := user.LookupGroupId(fmt.Sprintf("%d", stat.Gid)); err == nil {
		fs.Group = group.Name
	}

	if !info.IsDir() {
		digest, err := fileDigest(path)
		if err != nil {
			return nil, err
		}
		fs.Digest = digest
	}

	return fs, nil
}

func FileStatWithContext(ctx context.Context, path string) (*FileStat, error) {
	if !filesystemNeedsElevatedExecution(ctx) {
		return FileStatInfo(path)
	}

	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executor path for stat %s: %w", path, err)
	}

	result := CmdExec(ctx, executable, "--run-file-stat", path)
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("stat %s: %s", path, commandErrorText(result))
	}

	return decodePrivilegedFileStat(path, result.Stdout)
}

func WriteFileStat(path string, output io.Writer) error {
	stat, err := FileStatInfo(path)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(output).Encode(stat); err != nil {
		return fmt.Errorf("encode file stat %s: %w", path, err)
	}
	return nil
}

func WriteFileContents(path string, output io.Writer) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s for read helper: %w", path, err)
	}
	defer file.Close()

	if _, err := io.Copy(output, file); err != nil {
		return fmt.Errorf("stream %s to helper output: %w", path, err)
	}
	return nil
}

func WriteDirEntries(path string, output io.Writer) error {
	entries, err := ReadDir(path)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(output).Encode(entries); err != nil {
		return fmt.Errorf("encode directory entries %s: %w", path, err)
	}
	return nil
}

func WriteReadlinkTarget(path string, output io.Writer) error {
	target, err := FileReadlink(path)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(output, target); err != nil {
		return fmt.Errorf("write readlink target %s: %w", path, err)
	}
	return nil
}

func WriteFileFromReader(path string, input io.Reader, mode os.FileMode) error {
	data, err := io.ReadAll(input)
	if err != nil {
		return fmt.Errorf("read helper input for %s: %w", path, err)
	}
	if err := FileWrite(path, string(data), mode); err != nil {
		return err
	}
	return nil
}

func decodePrivilegedDirEntries(path, output string) ([]DirEntry, error) {
	var entries []DirEntry
	if err := json.Unmarshal([]byte(output), &entries); err != nil {
		return nil, fmt.Errorf("decode privileged dir read %s: %w", path, err)
	}
	return entries, nil
}

func decodePrivilegedFileStat(path, output string) (*FileStat, error) {
	var stat FileStat
	if err := json.Unmarshal([]byte(output), &stat); err != nil {
		return nil, fmt.Errorf("decode privileged stat %s: %w", path, err)
	}
	if strings.TrimSpace(stat.Path) == "" {
		stat.Path = path
	}
	return &stat, nil
}

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s for digest: %w", path, err)
	}
	defer f.Close()

	digest, err := digestutil.DigestReader(digestutil.AlgorithmBlake3, f)
	if err != nil {
		return "", fmt.Errorf("digest %s: %w", path, err)
	}
	return digest, nil
}

// FileChown changes ownership of a file.
func FileChown(path string, uid, gid int) error {
	return os.Chown(path, uid, gid)
}

func FileChownWithContext(ctx context.Context, path string, uid, gid int) error {
	if !filesystemNeedsElevatedExecution(ctx) {
		return FileChown(path, uid, gid)
	}

	result := CmdExec(ctx, "chown", fmt.Sprintf("%d:%d", uid, gid), path)
	if result.ExitCode != 0 {
		return fmt.Errorf("chown %s: %s", path, commandErrorText(result))
	}
	return nil
}

// FileChmod changes permissions of a file.
func FileChmod(path string, mode os.FileMode) error {
	return os.Chmod(path, mode)
}

func FileChmodWithContext(ctx context.Context, path string, mode os.FileMode) error {
	if !filesystemNeedsElevatedExecution(ctx) {
		return FileChmod(path, mode)
	}

	result := CmdExec(ctx, "chmod", fmt.Sprintf("%04o", mode.Perm()), path)
	if result.ExitCode != 0 {
		return fmt.Errorf("chmod %s: %s", path, commandErrorText(result))
	}
	return nil
}

// FileLock acquires an exclusive flock on the given path. The caller must
// call FileUnlock with the returned file descriptor when done.
func FileLock(path string) (*os.File, error) {
	lockPath := lockFilePath(path)
	if err := ensureWritableLockDir(filepath.Dir(lockPath)); err != nil {
		return nil, fmt.Errorf("create lock directory for %s: %w", path, err)
	}

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, fileLockFileMode)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", lockPath, err)
	}
	if err := os.Chmod(lockPath, fileLockFileMode); err != nil && !errors.Is(err, os.ErrPermission) {
		f.Close()
		return nil, fmt.Errorf("chmod lock file %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("flock %s: %w", lockPath, err)
	}
	return f, nil
}

func FileLockWithContext(_ context.Context, path string) (*os.File, error) {
	return FileLock(path)
}

func ensureWritableLockDir(path string) error {
	if err := os.MkdirAll(path, fileLockDirMode); err != nil {
		return err
	}
	if err := os.Chmod(path, fileLockDirMode); err != nil {
		if !errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("chmod lock directory %s: %w", path, err)
		}
		if verifyErr := verifyWritableLockDir(path); verifyErr != nil {
			return fmt.Errorf("chmod lock directory %s: %w", path, err)
		}
		return nil
	}
	return verifyWritableLockDir(path)
}

func verifyWritableLockDir(path string) error {
	probePath := filepath.Join(path, fmt.Sprintf(".lock-probe-%d", os.Getpid()))
	file, err := os.OpenFile(probePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("verify writable lock directory %s: %w", path, err)
	}
	if closeErr := file.Close(); closeErr != nil {
		_ = os.Remove(probePath)
		return fmt.Errorf("close lock directory probe %s: %w", probePath, closeErr)
	}
	if err := os.Remove(probePath); err != nil {
		return fmt.Errorf("remove lock directory probe %s: %w", probePath, err)
	}
	return nil
}

// FileUnlock releases the flock and closes the file.
func FileUnlock(f *os.File) error {
	if f == nil {
		return nil
	}
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func filesystemNeedsElevatedExecution(ctx context.Context) bool {
	execution, ok := ExecutionContextFromContext(ctx)
	if !ok {
		return false
	}

	if execution.BecomeUser == "" || execution.BecomeUser == "root" {
		return os.Geteuid() != 0
	}

	current, err := user.Current()
	if err != nil {
		return true
	}

	return current.Username != execution.BecomeUser
}

func lockFilePath(path string) string {
	digest := digestutil.MustDigestBytes(digestutil.AlgorithmXXH3_128, []byte(path))
	return filepath.Join(os.TempDir(), fileLockDirName, digestutil.Token(digest)+".lock")
}

func commandErrorText(result CmdResult) string {
	if result.Stderr != "" {
		return result.Stderr
	}
	return result.Stdout
}
