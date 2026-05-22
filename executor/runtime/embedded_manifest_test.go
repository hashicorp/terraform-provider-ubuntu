package runtime

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/assetmanifest"
)

func TestLoadEmbeddedManifestErrors(t *testing.T) {
	tests := []struct {
		name     string
		base64   string
		provider string
		wantErr  string
	}{
		{
			name:     "missing manifest",
			provider: "ubuntu",
			wantErr:  "executor digest manifest not embedded",
		},
		{
			name:     "invalid base64",
			base64:   "%",
			provider: "ubuntu",
			wantErr:  "decode embedded digest manifest:",
		},
		{
			name:     "invalid json",
			base64:   base64.StdEncoding.EncodeToString([]byte("{")),
			provider: "ubuntu",
			wantErr:  "unmarshal embedded digest manifest:",
		},
		{
			name:     "unsupported version",
			base64:   mustEncodeEmbeddedManifest(t, assetmanifest.Manifest{Version: 99, Provider: "ubuntu"}),
			provider: "ubuntu",
			wantErr:  "unsupported embedded digest manifest version 99",
		},
		{
			name:     "provider mismatch",
			base64:   mustEncodeEmbeddedManifest(t, assetmanifest.Manifest{Version: assetmanifest.ManifestVersion, Provider: "rocky"}),
			provider: "ubuntu",
			wantErr:  `embedded digest manifest provider mismatch: want "ubuntu", got "rocky"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withEmbeddedManifestState(t, tc.base64, tc.provider, func() {
				manifest, err := loadEmbeddedManifest()
				if err == nil || (err.Error() != tc.wantErr && !strings.Contains(err.Error(), tc.wantErr)) {
					t.Fatalf("loadEmbeddedManifest() error = %v, want %q", err, tc.wantErr)
				}
				if !reflect.DeepEqual(manifest, embeddedManifest) {
					t.Fatalf("manifest = %#v, want cached embedded manifest %#v", manifest, embeddedManifest)
				}
			})
		})
	}
}

func TestLoadEmbeddedManifestSuccessAndCache(t *testing.T) {
	want := assetmanifest.Manifest{
		Version:        assetmanifest.ManifestVersion,
		Provider:       "ubuntu",
		ExecutorArches: []string{"amd64", "arm64"},
		Plugins: map[string]assetmanifest.PluginManifestRecord{
			"linux_commands": {
				Compression:         assetmanifest.CompressionZstd,
				CompressedDigests:   map[string]string{"blake3": "abc", "shake256": "def"},
				UncompressedDigests: map[string]string{"blake3": "ghi", "shake256": "jkl"},
			},
		},
	}

	withEmbeddedManifestState(t, mustEncodeEmbeddedManifest(t, want), "ubuntu", func() {
		got, err := loadEmbeddedManifest()
		if err != nil {
			t.Fatalf("loadEmbeddedManifest() returned error: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("loadEmbeddedManifest() = %#v, want %#v", got, want)
		}

		embeddedManifestBase64 = "%"
		embeddedManifestProvider = "rocky"

		cached, err := loadEmbeddedManifest()
		if err != nil {
			t.Fatalf("cached loadEmbeddedManifest() returned error: %v", err)
		}
		if !reflect.DeepEqual(cached, want) {
			t.Fatalf("cached loadEmbeddedManifest() = %#v, want %#v", cached, want)
		}
	})
}

func TestNewDispatcherUsesEmbeddedManifestState(t *testing.T) {
	want := assetmanifest.Manifest{Version: assetmanifest.ManifestVersion, Provider: "ubuntu"}

	withEmbeddedManifestState(t, mustEncodeEmbeddedManifest(t, want), "ubuntu", func() {
		dispatcher := NewDispatcher(nil)
		if dispatcher.manifestErr != nil {
			t.Fatalf("NewDispatcher().manifestErr = %v, want nil", dispatcher.manifestErr)
		}
		if !reflect.DeepEqual(dispatcher.manifest, want) {
			t.Fatalf("NewDispatcher().manifest = %#v, want %#v", dispatcher.manifest, want)
		}
	})

	withEmbeddedManifestState(t, "", "ubuntu", func() {
		dispatcher := NewDispatcher(nil)
		if dispatcher.manifestErr == nil || dispatcher.manifestErr.Error() != "executor digest manifest not embedded" {
			t.Fatalf("NewDispatcher().manifestErr = %v, want missing embedded manifest error", dispatcher.manifestErr)
		}
	})
}

func withEmbeddedManifestState(t *testing.T, base64Value, provider string, fn func()) {
	t.Helper()

	savedBase64 := embeddedManifestBase64
	savedProvider := embeddedManifestProvider
	savedManifest := embeddedManifest
	savedErr := embeddedManifestErr

	embeddedManifestBase64 = base64Value
	embeddedManifestProvider = provider
	embeddedManifestOnce = sync.Once{}
	embeddedManifest = assetmanifest.Manifest{}
	embeddedManifestErr = nil

	defer func() {
		embeddedManifestBase64 = savedBase64
		embeddedManifestProvider = savedProvider
		embeddedManifestOnce = sync.Once{}
		embeddedManifest = savedManifest
		embeddedManifestErr = savedErr
	}()

	fn()
}

func mustEncodeEmbeddedManifest(t *testing.T, manifest assetmanifest.Manifest) string {
	t.Helper()

	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal(manifest) returned error: %v", err)
	}
	return base64.StdEncoding.EncodeToString(data)
}
