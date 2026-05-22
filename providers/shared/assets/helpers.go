package assets

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/assetmanifest"
)

const (
	ScopedExecutorsDirName = "scoped-executors"

	embeddedExecutorsDir = "executors"
	embeddedPluginsDir   = "plugins"
)

type assetCache struct {
	mu        sync.Mutex
	executors map[string]Asset
	plugins   map[string]Asset
}

func newAssetCache() assetCache {
	return assetCache{
		executors: make(map[string]Asset),
		plugins:   make(map[string]Asset),
	}
}

func (c *assetCache) executor(arch string) (Asset, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	asset, ok := c.executors[arch]
	return asset, ok
}

func (c *assetCache) storeExecutor(arch string, asset Asset) Asset {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.executors[arch] = asset
	return asset
}

func (c *assetCache) plugin(name string) (Asset, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	asset, ok := c.plugins[name]
	return asset, ok
}

func (c *assetCache) storePlugin(name string, asset Asset) Asset {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.plugins[name] = asset
	return asset
}

func newAsset(data []byte) Asset {
	return newAssetWithCompression(data, "")
}

func newAssetWithCompression(data []byte, compression string) Asset {
	return newAssetWithDigests(data, compression, assetmanifest.MustDigestSet(data))
}

func newAssetWithDigests(data []byte, compression string, digests map[string]string) Asset {
	digests = cloneDigests(digests)
	return Asset{
		Bytes:       data,
		Digest:      digests[assetmanifest.ConventionalDigestAlgorithm],
		Digests:     digests,
		Compression: compression,
	}
}

func cloneDigests(digests map[string]string) map[string]string {
	if len(digests) == 0 {
		return nil
	}
	clone := make(map[string]string, len(digests))
	for algorithm, digest := range digests {
		clone[algorithm] = digest
	}
	return clone
}

func executorFileName(arch string) string {
	return fmt.Sprintf("executor-linux-%s", arch)
}

func compressedExecutorFileName(arch string) string {
	return executorFileName(arch) + ".gz"
}

func pluginFileName(name string) string {
	return fmt.Sprintf("%s.wasm", name)
}

func compressedPluginFileName(name string) string {
	return fmt.Sprintf("%s.wasm.zst", name)
}

func ScopedProviderArtifactsDir(distRoot, provider string) string {
	return filepath.Join(distRoot, ScopedExecutorsDirName, provider)
}

func ScopedExecutorsDir(distRoot, provider string) string {
	return filepath.Join(ScopedProviderArtifactsDir(distRoot, provider), embeddedExecutorsDir)
}

func ScopedPluginsDir(distRoot, provider string) string {
	return filepath.Join(ScopedProviderArtifactsDir(distRoot, provider), embeddedPluginsDir)
}

func ScopedExecutorBinaryPath(distRoot, provider, arch string) string {
	return filepath.Join(ScopedExecutorsDir(distRoot, provider), executorFileName(arch))
}

func ScopedManifestPath(distRoot, provider string) string {
	return filepath.Join(ScopedProviderArtifactsDir(distRoot, provider), "manifest.json")
}

func ScopedCompressedPluginPath(distRoot, provider, name string) string {
	return filepath.Join(ScopedPluginsDir(distRoot, provider), compressedPluginFileName(name))
}

func joinItems(items []string) string {
	return strings.Join(items, ", ")
}
