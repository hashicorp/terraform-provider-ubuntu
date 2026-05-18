package assets

import (
	"fmt"
	"strings"
	"sync"
)

const (
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
	return Asset{
		Bytes:       data,
		Digest:      DigestBytes(data),
		Compression: compression,
	}
}

func executorFileName(arch string) string {
	return fmt.Sprintf("executor-linux-%s", arch)
}

func pluginFileName(name string) string {
	return fmt.Sprintf("%s.wasm", name)
}

func compressedPluginFileName(name string) string {
	return fmt.Sprintf("%s.wasm.zst", name)
}

func joinItems(items []string) string {
	return strings.Join(items, ", ")
}
