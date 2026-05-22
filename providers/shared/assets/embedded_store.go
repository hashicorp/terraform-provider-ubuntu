package assets

import (
	"errors"
	"fmt"
	"io/fs"
	"path"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/assetmanifest"
)

type EmbeddedStore struct {
	fsys  fs.FS
	root  string
	spec  Spec
	cache assetCache
}

func NewEmbeddedStore(fsys fs.FS, root string, spec Spec) *EmbeddedStore {
	return &EmbeddedStore{
		fsys:  fsys,
		root:  root,
		spec:  spec,
		cache: newAssetCache(),
	}
}

func (s *EmbeddedStore) Validate() error {
	missing := &MissingAssetsError{}
	for _, arch := range s.spec.ExecutorArches {
		if err := s.checkExecutorReadable(arch); err != nil {
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

func (s *EmbeddedStore) ExecutorBinary(arch string) (Asset, error) {
	if asset, ok := s.cache.executor(arch); ok {
		return asset, nil
	}
	asset, err := s.readExecutor(arch)
	if err != nil {
		return Asset{}, err
	}
	return s.cache.storeExecutor(arch, asset), nil
}

func (s *EmbeddedStore) PluginModule(name string) (Asset, error) {
	if asset, ok := s.cache.plugin(name); ok {
		return asset, nil
	}
	asset, err := s.readPlugin(name)
	if err != nil {
		return Asset{}, err
	}
	return s.cache.storePlugin(name, asset), nil
}

func (s *EmbeddedStore) checkReadable(assetPath string) error {
	file, err := s.fsys.Open(assetPath)
	if err != nil {
		return err
	}
	return file.Close()
}

func (s *EmbeddedStore) checkExecutorReadable(arch string) error {
	if err := s.checkReadable(s.compressedExecutorPath(arch)); err == nil {
		return nil
	}
	return s.checkReadable(s.executorPath(arch))
}

func (s *EmbeddedStore) executorPath(arch string) string {
	return path.Join(s.root, embeddedExecutorsDir, executorFileName(arch))
}

func (s *EmbeddedStore) compressedExecutorPath(arch string) string {
	return path.Join(s.root, embeddedExecutorsDir, compressedExecutorFileName(arch))
}

func (s *EmbeddedStore) compressedPluginPath(name string) string {
	return path.Join(s.root, embeddedPluginsDir, compressedPluginFileName(name))
}

func (s *EmbeddedStore) manifestPath() string {
	return path.Join(s.root, "manifest.json")
}

func (s *EmbeddedStore) readExecutor(arch string) (Asset, error) {
	compressedPath := s.compressedExecutorPath(arch)
	data, err := fs.ReadFile(s.fsys, compressedPath)
	if err == nil {
		return Asset{Bytes: data, Compression: assetmanifest.CompressionGzip}, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return Asset{}, fmt.Errorf("read embedded compressed executor %q: %w", arch, err)
	}

	data, err = fs.ReadFile(s.fsys, s.executorPath(arch))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Asset{}, fmt.Errorf("no executor binary for architecture %q", arch)
		}
		return Asset{}, fmt.Errorf("read embedded executor %q: %w", arch, err)
	}
	return newAsset(data), nil
}

func (s *EmbeddedStore) readPlugin(name string) (Asset, error) {
	compressedPath := s.compressedPluginPath(name)
	data, err := fs.ReadFile(s.fsys, compressedPath)
	if err == nil {
		return newAssetWithCompression(data, assetmanifest.CompressionZstd), nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return Asset{}, fmt.Errorf("unknown plugin %q", name)
	}
	return Asset{}, fmt.Errorf("read embedded compressed plugin %q: %w", name, err)
}
