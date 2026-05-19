//go:build wasm

package capabilities

import (
	"context"
	"fmt"
	"os"
)

func FileRead(path string) (string, error) {
	return "", fmt.Errorf("host file operations are unavailable on wasm")
}

func FileReadWithContext(ctx context.Context, path string) (string, error) {
	_ = ctx
	return FileRead(path)
}

func FileWrite(path string, content string, mode os.FileMode) error {
	return fmt.Errorf("host file operations are unavailable on wasm")
}

func FileWriteWithContext(ctx context.Context, path string, content string, mode os.FileMode) error {
	_ = ctx
	return FileWrite(path, content, mode)
}

func FileExists(path string) bool {
	return false
}

func FileStatInfo(path string) (*FileStat, error) {
	return nil, fmt.Errorf("host file operations are unavailable on wasm")
}

func FileStatWithContext(ctx context.Context, path string) (*FileStat, error) {
	_ = ctx
	return FileStatInfo(path)
}

func FileChown(path string, uid, gid int) error {
	return fmt.Errorf("host file operations are unavailable on wasm")
}

func FileChownWithContext(ctx context.Context, path string, uid, gid int) error {
	_ = ctx
	return FileChown(path, uid, gid)
}

func FileChmod(path string, mode os.FileMode) error {
	return fmt.Errorf("host file operations are unavailable on wasm")
}

func FileChmodWithContext(ctx context.Context, path string, mode os.FileMode) error {
	_ = ctx
	return FileChmod(path, mode)
}

func FileLock(path string) (*os.File, error) {
	return nil, fmt.Errorf("host file operations are unavailable on wasm")
}

func FileLockWithContext(ctx context.Context, path string) (*os.File, error) {
	_ = ctx
	return FileLock(path)
}

func FileUnlock(f *os.File) error {
	return fmt.Errorf("host file operations are unavailable on wasm")
}

func filesystemNeedsElevatedExecution(ctx context.Context) bool {
	_ = ctx
	return false
}

func commandErrorText(result CmdResult) string {
	if result.Stderr != "" {
		return result.Stderr
	}
	return result.Stdout
}
