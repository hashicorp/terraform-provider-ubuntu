package assetmanifest

import (
	"fmt"

	digestutil "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/digest"
)

const ManifestVersion = 1

const (
	ConventionalDigestAlgorithm = digestutil.AlgorithmBlake3
	PostQuantumDigestAlgorithm  = digestutil.AlgorithmShake256
)

// Canonical compression labels used in Manifest records and on assets.Asset values.
const (
	CompressionGzip = "gzip"
	CompressionZstd = "zstd"
)

var ManifestDigestAlgorithms = []string{
	digestutil.AlgorithmBlake3,
	digestutil.AlgorithmShake256,
}

type Manifest struct {
	Version        int                               `json:"version"`
	Provider       string                            `json:"provider"`
	ExecutorArches []string                          `json:"executor_arches"`
	Executors      map[string]ExecutorManifestRecord `json:"executors,omitempty"`
	Plugins        map[string]PluginManifestRecord   `json:"plugins"`
}

type ExecutorManifestRecord struct {
	Digests map[string]string `json:"digests,omitempty"`
}

type PluginManifestRecord struct {
	Compression         string            `json:"compression,omitempty"`
	CompressedDigests   map[string]string `json:"compressed_digests,omitempty"`
	UncompressedDigests map[string]string `json:"uncompressed_digests,omitempty"`
}

func DigestAlgorithmForSelection(usePostQuantum bool) string {
	if usePostQuantum {
		return PostQuantumDigestAlgorithm
	}
	return ConventionalDigestAlgorithm
}

func (m Manifest) Executor(arch string) (ExecutorManifestRecord, error) {
	if m.Executors == nil {
		return ExecutorManifestRecord{}, fmt.Errorf("executor digest manifest is empty")
	}
	record, ok := m.Executors[arch]
	if !ok {
		return ExecutorManifestRecord{}, fmt.Errorf("executor digest manifest missing entry for %q", arch)
	}
	return record, nil
}

func (m Manifest) Plugin(name string) (PluginManifestRecord, error) {
	if m.Plugins == nil {
		return PluginManifestRecord{}, fmt.Errorf("plugin digest manifest is empty")
	}
	record, ok := m.Plugins[name]
	if !ok {
		return PluginManifestRecord{}, fmt.Errorf("plugin digest manifest missing entry for %q", name)
	}
	return record, nil
}

func (r ExecutorManifestRecord) Digest(algorithm string) (string, error) {
	return manifestDigest(r.Digests, algorithm, "executor", "")
}

func (r PluginManifestRecord) CompressedDigest(algorithm string) (string, error) {
	return manifestDigest(r.CompressedDigests, algorithm, "plugin", "compressed")
}

func (r PluginManifestRecord) UncompressedDigest(algorithm string) (string, error) {
	return manifestDigest(r.UncompressedDigests, algorithm, "plugin", "uncompressed")
}

func manifestDigest(values map[string]string, algorithm, artifact, scope string) (string, error) {
	digest, ok := values[algorithm]
	if !ok || digest == "" {
		if scope == "" {
			return "", fmt.Errorf("%s digest manifest missing %s digest", artifact, algorithm)
		}
		return "", fmt.Errorf("%s digest manifest missing %s %s digest", artifact, scope, algorithm)
	}
	return digest, nil
}
