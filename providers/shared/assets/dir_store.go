package assets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/assetmanifest"
)

type DirStore struct {
	root          string
	executorsRoot string
	pluginsRoot   string
	spec          Spec
	cache         assetCache
}

func NewDirStore(root string, spec Spec) *DirStore {
	return &DirStore{
		root:          root,
		executorsRoot: resolveAssetRoot(root, embeddedExecutorsDir),
		pluginsRoot:   resolveAssetRoot(root, embeddedPluginsDir),
		spec:          spec,
		cache:         newAssetCache(),
	}
}

func (s *DirStore) Validate() error {
	missing := &MissingAssetsError{}
	for _, arch := range s.spec.ExecutorArches {
		if err := s.checkExecutorReadable(arch); err != nil {
			missing.Executors = append(missing.Executors, arch)
			continue
		}
		if _, err := s.executorDigests(arch); err != nil {
			missing.Executors = append(missing.Executors, arch)
		}
	}
	for _, name := range s.spec.PluginModules {
		if err := s.checkReadable(s.compressedPluginPath(name)); err != nil {
			missing.Plugins = append(missing.Plugins, name)
		}
	}
	if missing.empty() {
		return nil
	}
	return missing
}

func (s *DirStore) ExecutorBinary(arch string) (Asset, error) {
	if asset, ok := s.cache.executor(arch); ok {
		return asset, nil
	}
	asset, err := s.readExecutor(arch)
	if err != nil {
		return Asset{}, err
	}
	return s.cache.storeExecutor(arch, asset), nil
}

func (s *DirStore) PluginModule(name string) (Asset, error) {
	if asset, ok := s.cache.plugin(name); ok {
		return asset, nil
	}
	asset, err := s.readPlugin(name)
	if err != nil {
		return Asset{}, err
	}
	return s.cache.storePlugin(name, asset), nil
}

func (s *DirStore) checkReadable(assetPath string) error {
	file, err := os.Open(assetPath)
	if err != nil {
		return err
	}
	return file.Close()
}

func (s *DirStore) checkExecutorReadable(arch string) error {
	if err := s.checkReadable(s.compressedExecutorPath(arch)); err == nil {
		return nil
	}
	return s.checkReadable(s.executorPath(arch))
}

func (s *DirStore) executorPath(arch string) string {
	return filepath.Join(s.executorsRoot, executorFileName(arch))
}

func (s *DirStore) compressedExecutorPath(arch string) string {
	return filepath.Join(s.executorsRoot, compressedExecutorFileName(arch))
}

func (s *DirStore) compressedPluginPath(name string) string {
	return filepath.Join(s.pluginsRoot, compressedPluginFileName(name))
}

func (s *DirStore) manifestPath() string {
	return filepath.Join(s.root, "manifest.json")
}

func (s *DirStore) readManifest() (assetmanifest.Manifest, error) {
	data, err := os.ReadFile(s.manifestPath())
	if err != nil {
		return assetmanifest.Manifest{}, fmt.Errorf("read manifest from %s: %w", s.root, err)
	}
	var manifest assetmanifest.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return assetmanifest.Manifest{}, fmt.Errorf("decode manifest from %s: %w", s.root, err)
	}
	return manifest, nil
}

func (s *DirStore) executorDigests(arch string) (map[string]string, error) {
	manifest, err := s.readManifest()
	if err != nil {
		return nil, err
	}
	record, err := manifest.Executor(arch)
	if err != nil {
		return nil, err
	}
	return record.Digests, nil
}

func (s *DirStore) readExecutor(arch string) (Asset, error) {
	digests, err := s.executorDigests(arch)
	if err != nil {
		return Asset{}, err
	}

	compressedPath := s.compressedExecutorPath(arch)
	data, err := os.ReadFile(compressedPath)
	if err == nil {
		return newAssetWithDigests(data, assetmanifest.CompressionGzip, digests), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Asset{}, fmt.Errorf("read compressed executor %q from %s: %w", arch, s.executorsRoot, err)
	}

	data, err = os.ReadFile(s.executorPath(arch))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Asset{}, fmt.Errorf("no executor binary for architecture %q", arch)
		}
		return Asset{}, fmt.Errorf("read executor %q from %s: %w", arch, s.executorsRoot, err)
	}
	return newAssetWithDigests(data, "", digests), nil
}

func (s *DirStore) readPlugin(name string) (Asset, error) {
	compressedPath := s.compressedPluginPath(name)
	data, err := os.ReadFile(compressedPath)
	if err == nil {
		return newAssetWithCompression(data, assetmanifest.CompressionZstd), nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return Asset{}, fmt.Errorf("unknown plugin %q", name)
	}
	return Asset{}, fmt.Errorf("read compressed plugin %q from %s: %w", name, s.pluginsRoot, err)
}

func resolveAssetRoot(root, subdir string) string {
	candidate := filepath.Join(root, subdir)
	info, err := os.Stat(candidate)
	if err == nil && info.IsDir() {
		return candidate
	}
	return root
}
