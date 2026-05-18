package assets

import "fmt"

type MemoryStore struct {
	spec      Spec
	executors map[string]Asset
	plugins   map[string]Asset
}

func NewMemoryStore(spec Spec, executors map[string][]byte, plugins map[string][]byte) *MemoryStore {
	store := &MemoryStore{
		spec:      spec,
		executors: make(map[string]Asset, len(executors)),
		plugins:   make(map[string]Asset, len(plugins)),
	}
	for arch, data := range executors {
		store.executors[arch] = newAsset(data)
	}
	for name, data := range plugins {
		compressed, err := CompressPluginModule(data)
		if err != nil {
			panic(fmt.Sprintf("compress plugin %q: %v", name, err))
		}
		store.plugins[name] = newAssetWithCompression(compressed, CompressionZstd)
	}
	return store
}

func (s *MemoryStore) Validate() error {
	missing := &MissingAssetsError{}
	for _, arch := range s.spec.ExecutorArches {
		if _, ok := s.executors[arch]; !ok {
			missing.Executors = append(missing.Executors, arch)
		}
	}
	for _, name := range s.spec.PluginModules {
		if _, ok := s.plugins[name]; !ok {
			missing.Plugins = append(missing.Plugins, name)
		}
	}
	if missing.empty() {
		return nil
	}
	return missing
}

func (s *MemoryStore) ExecutorBinary(arch string) (Asset, error) {
	asset, ok := s.executors[arch]
	if !ok {
		return Asset{}, fmt.Errorf("no executor binary for architecture %q", arch)
	}
	return asset, nil
}

func (s *MemoryStore) PluginModule(name string) (Asset, error) {
	asset, ok := s.plugins[name]
	if !ok {
		return Asset{}, fmt.Errorf("unknown plugin %q", name)
	}
	return asset, nil
}
