package capabilities

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
)

// HostAPI provides the interface that WASM plugins use to interact with
// the host system. It holds the discovered profile and manages file locks.
type HostAPI struct {
	Profile HostProfile

	mu    sync.Mutex
	locks map[string]*os.File
}

// NewHostAPI creates a HostAPI with the given profile.
func NewHostAPI(profile HostProfile) *HostAPI {
	return &HostAPI{
		Profile: profile,
		locks:   make(map[string]*os.File),
	}
}

// --- File operations ---

func (h *HostAPI) FileRead(path string) (string, error) {
	return FileRead(path)
}

func (h *HostAPI) FileReadWithContext(ctx context.Context, path string) (string, error) {
	return FileReadWithContext(ctx, path)
}

func (h *HostAPI) FileWrite(ctx context.Context, path, content string, mode os.FileMode) error {
	return FileWriteWithContext(ctx, path, content, mode)
}

func (h *HostAPI) DirEnsure(ctx context.Context, path string, mode os.FileMode) error {
	return DirEnsureWithContext(ctx, path, mode)
}

func (h *HostAPI) ReadDir(ctx context.Context, path string) ([]DirEntry, error) {
	return ReadDirWithContext(ctx, path)
}

func (h *HostAPI) FileDelete(ctx context.Context, path string) error {
	return FileDeleteWithContext(ctx, path)
}

func (h *HostAPI) FileExists(path string) bool {
	return FileExists(path)
}

func (h *HostAPI) FileStat(path string) (*FileStat, error) {
	return FileStatInfo(path)
}

func (h *HostAPI) FileStatWithContext(ctx context.Context, path string) (*FileStat, error) {
	return FileStatWithContext(ctx, path)
}

func (h *HostAPI) FileChown(ctx context.Context, path string, uid, gid int) error {
	return FileChownWithContext(ctx, path, uid, gid)
}

func (h *HostAPI) FileChownNames(ctx context.Context, path, owner, group string) error {
	uid, gid, err := LookupOwnership(owner, group)
	if err == nil {
		return FileChownWithContext(ctx, path, uid, gid)
	}

	result := CmdExec(ctx, "chown", fmt.Sprintf("%s:%s", owner, group), path)
	if result.ExitCode != 0 {
		return fmt.Errorf("chown failed: %s", result.Stderr)
	}
	return nil
}

func (h *HostAPI) FileChmod(ctx context.Context, path string, mode os.FileMode) error {
	return FileChmodWithContext(ctx, path, mode)
}

func (h *HostAPI) FileLock(ctx context.Context, path string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.locks[path]; ok {
		return fmt.Errorf("already locked: %s", path)
	}
	f, err := FileLockWithContext(ctx, path)
	if err != nil {
		return err
	}
	h.locks[path] = f
	return nil
}

func (h *HostAPI) FileUnlock(path string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	f, ok := h.locks[path]
	if !ok {
		return fmt.Errorf("not locked: %s", path)
	}
	delete(h.locks, path)
	return FileUnlock(f)
}

func (h *HostAPI) FileRename(ctx context.Context, from, to string) error {
	return FileRenameWithContext(ctx, from, to)
}

func (h *HostAPI) FileReadlink(ctx context.Context, path string) (string, error) {
	return FileReadlinkWithContext(ctx, path)
}

func (h *HostAPI) FileSymlink(ctx context.Context, target, path string) error {
	return FileSymlinkWithContext(ctx, target, path)
}

func (h *HostAPI) LookupUser(name string) (*IdentityUser, error) {
	return LookupUser(name)
}

func (h *HostAPI) LookupGroup(name string) (*IdentityGroup, error) {
	return LookupGroup(name)
}

// --- Command execution ---

func (h *HostAPI) CmdExec(ctx context.Context, name string, args ...string) CmdResult {
	return CmdExec(ctx, name, args...)
}

func (h *HostAPI) CmdExists(name string) bool {
	return CmdExists(name)
}

// --- Profile helpers ---

func (h *HostAPI) HostProfileJSON() (string, error) {
	data, err := json.Marshal(h.Profile)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (h *HostAPI) FetchURL(ctx context.Context, url string) ([]byte, error) {
	return FetchURL(ctx, url)
}

func LookupOwnership(owner, group string) (int, int, error) {
	uid := -1
	gid := -1
	var err error

	if owner != "" {
		uid, err = strconv.Atoi(owner)
		if err != nil {
			uid, err = lookupUserID(owner)
			if err != nil {
				return 0, 0, err
			}
		}
	}

	if group != "" {
		gid, err = strconv.Atoi(group)
		if err != nil {
			gid, err = lookupGroupID(group)
			if err != nil {
				return 0, 0, err
			}
		}
	}

	if uid < 0 {
		uid = 0
	}
	if gid < 0 {
		gid = 0
	}
	return uid, gid, nil
}

// --- Logging ---

func (h *HostAPI) LogInfo(msg string) {
	log.Printf("[INFO] %s", msg)
}

func (h *HostAPI) LogWarn(msg string) {
	log.Printf("[WARN] %s", msg)
}
