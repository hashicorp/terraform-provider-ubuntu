// Copyright IBM Corp. 2026

package hostfs

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
)

type FileSnapshot struct {
	Path    string
	Exists  bool
	Content []byte
}

func CaptureSnapshot(path string) (*FileSnapshot, error) {
	exists, err := pluginsdk.FileExists(path)
	if err != nil {
		return nil, fmt.Errorf("check %s: %w", path, err)
	}
	snapshot := &FileSnapshot{Path: path, Exists: exists}
	if !exists {
		return snapshot, nil
	}
	content, err := pluginsdk.FileRead(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	snapshot.Content = append([]byte(nil), content...)
	return snapshot, nil
}

func RestoreSnapshot(snapshot *FileSnapshot, mode uint32) error {
	if snapshot == nil {
		return nil
	}
	if !snapshot.Exists {
		return CleanupFile(snapshot.Path)
	}
	if err := pluginsdk.FileWrite(snapshot.Path, snapshot.Content, mode); err != nil {
		return fmt.Errorf("restore %s: %w", snapshot.Path, err)
	}
	return nil
}

func RemoveIfExists(path string) (bool, error) {
	exists, err := pluginsdk.FileExists(path)
	if err != nil {
		return false, fmt.Errorf("check %s: %w", path, err)
	}
	if !exists {
		return false, nil
	}
	if err := pluginsdk.FileDelete(path); err != nil {
		return false, fmt.Errorf("delete %s: %w", path, err)
	}
	return true, nil
}

func TempPath(prefix, suffix string) string {
	prefix = sanitizeTempToken(prefix)
	if prefix == "" {
		prefix = "tf-linux-provider"
	}
	return filepath.Join("/tmp", fmt.Sprintf("%s-%d%s", prefix, time.Now().UnixNano(), suffix))
}

func WriteTempFile(prefix, suffix string, data []byte, mode uint32) (string, error) {
	path := TempPath(prefix, suffix)
	if err := pluginsdk.FileWrite(path, data, mode); err != nil {
		return "", fmt.Errorf("write temp file %s: %w", path, err)
	}
	return path, nil
}

func CleanupFile(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := pluginsdk.FileDelete(path); err != nil {
		return fmt.Errorf("delete %s: %w", path, err)
	}
	return nil
}

func sanitizeTempToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.NewReplacer("/", "-", " ", "-", "_", "-", string(filepath.Separator), "-").Replace(value)
	for strings.Contains(value, "--") {
		value = strings.ReplaceAll(value, "--", "-")
	}
	return strings.Trim(value, "-")
}
