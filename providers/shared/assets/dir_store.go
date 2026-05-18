package assets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		if err := s.checkReadable(s.executorPath(arch)); err != nil {
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
	data, err := os.ReadFile(s.executorPath(arch))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Asset{}, fmt.Errorf("no executor binary for architecture %q", arch)
		}
		return Asset{}, fmt.Errorf("read executor %q from %s: %w", arch, s.executorsRoot, err)
	}
	return s.cache.storeExecutor(arch, newAsset(data)), nil
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

func (s *DirStore) executorPath(arch string) string {
	return filepath.Join(s.executorsRoot, executorFileName(arch))
}

func (s *DirStore) pluginPath(name string) string {
	return filepath.Join(s.pluginsRoot, pluginFileName(name))
}

func (s *DirStore) compressedPluginPath(name string) string {
	return filepath.Join(s.pluginsRoot, compressedPluginFileName(name))
}

func (s *DirStore) readPlugin(name string) (Asset, error) {
	compressedPath := s.compressedPluginPath(name)
	data, err := os.ReadFile(compressedPath)
	if err == nil {
		return newAssetWithCompression(data, CompressionZstd), nil
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
