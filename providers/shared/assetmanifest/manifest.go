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
	Version        int                             `json:"version"`
	Provider       string                          `json:"provider"`
	ExecutorArches []string                        `json:"executor_arches"`
	Plugins        map[string]PluginManifestRecord `json:"plugins"`
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

func (r PluginManifestRecord) CompressedDigest(algorithm string) (string, error) {
	return pluginDigest(r.CompressedDigests, algorithm, "compressed")
}

func (r PluginManifestRecord) UncompressedDigest(algorithm string) (string, error) {
	return pluginDigest(r.UncompressedDigests, algorithm, "uncompressed")
}

func pluginDigest(values map[string]string, algorithm, scope string) (string, error) {
	digest, ok := values[algorithm]
	if !ok || digest == "" {
		return "", fmt.Errorf("plugin digest manifest missing %s %s digest", scope, algorithm)
	}
	return digest, nil
}
