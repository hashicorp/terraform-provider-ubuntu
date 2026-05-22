package assetmanifest

import (
	"fmt"

	digestutil "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/digest"
)

type PluginContent struct {
	Uncompressed []byte
	Compressed   []byte
}

func BuildWithPluginContent(provider string, executorArches, pluginModules []string, loadPlugin func(name string) (PluginContent, error)) (Manifest, error) {
	plugins := make(map[string]PluginManifestRecord, len(pluginModules))
	for _, module := range pluginModules {
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
		record, err := NewPluginRecord(content.Uncompressed, content.Compressed)
		if err != nil {
			return Manifest{}, fmt.Errorf("build plugin manifest record %s: %w", module, err)
		}
		plugins[module] = record
	}

	return Manifest{
		Version:        ManifestVersion,
		Provider:       provider,
		ExecutorArches: append([]string(nil), executorArches...),
		Plugins:        plugins,
	}, nil
}

func NewPluginRecord(uncompressed, compressed []byte) (PluginManifestRecord, error) {
	compressedDigests, err := DigestSet(compressed)
	if err != nil {
		return PluginManifestRecord{}, err
	}
	uncompressedDigests, err := DigestSet(uncompressed)
	if err != nil {
		return PluginManifestRecord{}, err
	}
	return PluginManifestRecord{
		Compression:         CompressionZstd,
		CompressedDigests:   compressedDigests,
		UncompressedDigests: uncompressedDigests,
	}, nil
}

func NewExecutorRecord(data []byte) (ExecutorManifestRecord, error) {
	digests, err := DigestSet(data)
	if err != nil {
		return ExecutorManifestRecord{}, err
	}
	return ExecutorManifestRecord{Digests: digests}, nil
}

func (m *Manifest) SetExecutor(arch string, data []byte) error {
	record, err := NewExecutorRecord(data)
	if err != nil {
		return err
	}
	if m.Executors == nil {
		m.Executors = make(map[string]ExecutorManifestRecord)
	}
	m.Executors[arch] = record
	return nil
}

func DigestSet(data []byte) (map[string]string, error) {
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

func MustDigestSet(data []byte) map[string]string {
	digests, err := DigestSet(data)
	if err != nil {
		panic(err)
	}
	return digests
}
