package assets

import (
	"fmt"
	"path/filepath"

	digestutil "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/digest"
)

func BuildManifest(provider string, spec Spec, loadPlugin func(name string) ([]byte, error)) (Manifest, error) {
	return BuildManifestWithPluginContent(provider, spec, func(name string) (PluginManifestContent, error) {
		data, err := loadPlugin(name)
		if err != nil {
			return PluginManifestContent{}, err
		}
		compressed, err := CompressPluginModule(data)
		if err != nil {
			return PluginManifestContent{}, err
		}
		return PluginManifestContent{Uncompressed: data, Compressed: compressed}, nil
	})
}

type PluginManifestContent struct {
	Uncompressed []byte
	Compressed   []byte
}

func BuildManifestWithPluginContent(provider string, spec Spec, loadPlugin func(name string) (PluginManifestContent, error)) (Manifest, error) {
	plugins := make(map[string]PluginManifestRecord, len(spec.PluginModules))
	for _, module := range spec.PluginModules {
		content, err := loadPlugin(module)
		if err != nil {
			return Manifest{}, fmt.Errorf("load plugin %s: %w", module, err)
		}
		if len(content.Uncompressed) == 0 {
			return Manifest{}, fmt.Errorf("load plugin %s: empty uncompressed bytes", module)
		}
		if len(content.Compressed) == 0 {
			return Manifest{}, fmt.Errorf("load plugin %s: empty compressed bytes", module)
		}
		record, err := NewPluginManifestRecord(content.Uncompressed, content.Compressed)
		if err != nil {
			return Manifest{}, fmt.Errorf("build plugin manifest record %s: %w", module, err)
		}
		plugins[module] = record
	}

	return Manifest{
		Version:        ManifestVersion,
		Provider:       provider,
		ExecutorArches: append([]string(nil), spec.ExecutorArches...),
		Plugins:        plugins,
	}, nil
}

func NewPluginManifestRecord(uncompressed, compressed []byte) (PluginManifestRecord, error) {
	compressedDigests, err := digestSet(compressed)
	if err != nil {
		return PluginManifestRecord{}, err
	}
	uncompressedDigests, err := digestSet(uncompressed)
	if err != nil {
		return PluginManifestRecord{}, err
	}
	return PluginManifestRecord{
		Compression:         CompressionZstd,
		CompressedDigests:   compressedDigests,
		UncompressedDigests: uncompressedDigests,
	}, nil
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

func digestSet(data []byte) (map[string]string, error) {
	result := make(map[string]string, len(ManifestDigestAlgorithms))
	for _, algorithm := range ManifestDigestAlgorithms {
		digest, err := digestutil.DigestBytes(algorithm, data)
		if err != nil {
			return nil, err
		}
		result[algorithm] = digest
	}
	return result, nil
}
