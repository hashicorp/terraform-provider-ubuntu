package runtime

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/assetmanifest"
)

var (
	embeddedManifestBase64   string
	embeddedManifestProvider string

	embeddedManifestOnce sync.Once
	embeddedManifest     assetmanifest.Manifest
	embeddedManifestErr  error
)

func loadEmbeddedManifest() (assetmanifest.Manifest, error) {
	embeddedManifestOnce.Do(func() {
		if strings.TrimSpace(embeddedManifestBase64) == "" {
			embeddedManifestErr = fmt.Errorf("executor digest manifest not embedded")
			return
		}

		data, err := base64.StdEncoding.DecodeString(embeddedManifestBase64)
		if err != nil {
			embeddedManifestErr = fmt.Errorf("decode embedded digest manifest: %w", err)
			return
		}

		if err := json.Unmarshal(data, &embeddedManifest); err != nil {
			embeddedManifestErr = fmt.Errorf("unmarshal embedded digest manifest: %w", err)
			return
		}
		if embeddedManifest.Version != assetmanifest.ManifestVersion {
			embeddedManifestErr = fmt.Errorf("unsupported embedded digest manifest version %d", embeddedManifest.Version)
			return
		}
		if strings.TrimSpace(embeddedManifestProvider) != "" && embeddedManifest.Provider != embeddedManifestProvider {
			embeddedManifestErr = fmt.Errorf("embedded digest manifest provider mismatch: want %q, got %q", embeddedManifestProvider, embeddedManifest.Provider)
		}
	})

	return embeddedManifest, embeddedManifestErr
}
