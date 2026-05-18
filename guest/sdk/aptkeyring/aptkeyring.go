package aptkeyring

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
	"golang.org/x/crypto/openpgp/armor"
)

const (
	DefaultSourcesListPath    = "/etc/apt/sources.list"
	DefaultSourcesListDirPath = "/etc/apt/sources.list.d"
	KeyringDirMode            = 0o755
	KeyringFileMode           = 0o644
)

func Install(url, path string) error {
	url = strings.TrimSpace(url)
	path = strings.TrimSpace(path)
	if url == "" {
		return fmt.Errorf("apt keyring url must not be empty")
	}
	if path == "" {
		return fmt.Errorf("apt keyring path must not be empty")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("apt keyring path must be absolute, got %q", path)
	}

	raw, err := pluginsdk.FetchURL(url)
	if err != nil {
		return err
	}
	dearmored, err := Normalize(raw)
	if err != nil {
		return err
	}
	if err := pluginsdk.DirEnsure(filepath.Dir(path), KeyringDirMode); err != nil {
		return err
	}
	if err := pluginsdk.FileWrite(path, dearmored, KeyringFileMode); err != nil {
		return fmt.Errorf("write apt keyring %s: %w", path, err)
	}
	return nil
}

func Referenced(path string) (bool, error) {
	return ReferencedInPaths(path, DefaultSourcesListPath, DefaultSourcesListDirPath)
}

func ReferencedInPaths(keyringPath, sourcesListPath, sourcesDir string) (bool, error) {
	keyringPath = strings.TrimSpace(keyringPath)
	if keyringPath == "" {
		return false, nil
	}

	candidates := make([]string, 0, 8)
	if strings.TrimSpace(sourcesListPath) != "" {
		candidates = append(candidates, sourcesListPath)
	}
	if strings.TrimSpace(sourcesDir) != "" {
		entries, err := pluginsdk.ReadDir(sourcesDir)
		if err != nil {
			if !isNotExistError(err) {
				return false, fmt.Errorf("read apt sources directory %s: %w", sourcesDir, err)
			}
		} else {
			for _, entry := range entries {
				if entry.IsDir {
					continue
				}
				candidates = append(candidates, filepath.Join(sourcesDir, entry.Name))
			}
		}
	}

	for _, candidate := range candidates {
		content, err := pluginsdk.FileRead(candidate)
		if err != nil {
			if isNotExistError(err) {
				continue
			}
			return false, fmt.Errorf("read apt source %s: %w", candidate, err)
		}
		if ReferencedInContent(string(content), keyringPath) {
			return true, nil
		}
	}

	return false, nil
}

func ReferencedInContent(content, keyringPath string) bool {
	return strings.Contains(content, strings.TrimSpace(keyringPath))
}

func Normalize(raw []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("apt keyring content is empty")
	}

	block, err := armor.Decode(bytes.NewReader(trimmed))
	if err == nil {
		dearmored := new(bytes.Buffer)
		if _, err := dearmored.ReadFrom(block.Body); err != nil {
			return nil, fmt.Errorf("read armored apt keyring: %w", err)
		}
		if dearmored.Len() == 0 {
			return nil, fmt.Errorf("armored apt keyring did not contain any packets")
		}
		return dearmored.Bytes(), nil
	}
	if looksLikeArmoredPGP(trimmed) {
		return nil, fmt.Errorf("decode armored apt keyring: %w", err)
	}
	return trimmed, nil
}

func looksLikeArmoredPGP(data []byte) bool {
	prefix := string(data)
	if len(prefix) > 64 {
		prefix = prefix[:64]
	}
	return strings.Contains(prefix, "BEGIN PGP")
}

func isNotExistError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no such file or directory") || strings.Contains(text, "not found")
}
