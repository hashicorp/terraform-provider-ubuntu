// Copyright IBM Corp. 2026

package assets

import (
	"bytes"
	"fmt"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/assetmanifest"
)

// zstdFrameMagic is the first four bytes of a well-formed zstd frame
// (RFC 8478 §3.1.1).
var zstdFrameMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}

type MemoryStore struct {
	spec      Spec
	executors map[string]Asset
	plugins   map[string]Asset
}

// NewMemoryStore returns an in-memory [Store] backed by the supplied executor
// and plugin byte maps.
//
// Plugin entries must already be zstd-encoded; NewMemoryStore does not
// compress them. Supplying anything other than a zstd frame is a programming
// error and panics, because such bytes would later be sent to the executor and
// fail zstd decoding far from the caller.
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
		if !bytes.HasPrefix(data, zstdFrameMagic) {
			panic(fmt.Sprintf("assets.NewMemoryStore: plugin %q must be zstd-encoded (got %d bytes with prefix %x)", name, len(data), data[:minInt(len(data), 4)]))
		}
		store.plugins[name] = newAssetWithCompression(data, assetmanifest.CompressionZstd)
	}
	return store
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
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
	if !missing.empty() {
		return missing
	}
	return nil
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
