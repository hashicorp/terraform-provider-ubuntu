package plugincodec

import (
	"bytes"
	"testing"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/assetmanifest"
)

func TestDecompressExecutorBinaryReturnsRawBytes(t *testing.T) {
	t.Parallel()

	want := []byte("raw executor")
	got, err := DecompressExecutorBinary(want, "")
	if err != nil {
		t.Fatalf("DecompressExecutorBinary() returned error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("raw executor = %q, want %q", string(got), string(want))
	}
}

func TestDecompressExecutorBinaryRejectsUnsupportedCompression(t *testing.T) {
	t.Parallel()

	if _, err := DecompressExecutorBinary([]byte("executor"), "br"); err == nil {
		t.Fatal("DecompressExecutorBinary() should reject unsupported compression")
	}
}

func TestExecutorGzipCompressionRoundTrip(t *testing.T) {
	t.Parallel()

	want := bytes.Repeat([]byte("executor payload "), 64)
	compressed, err := CompressExecutorBinary(want)
	if err != nil {
		t.Fatalf("CompressExecutorBinary() returned error: %v", err)
	}
	if len(compressed) >= len(want) {
		t.Fatalf("compressed executor length = %d, want smaller than %d", len(compressed), len(want))
	}
	got, err := DecompressExecutorBinary(compressed, assetmanifest.CompressionGzip)
	if err != nil {
		t.Fatalf("DecompressExecutorBinary() returned error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("decompressed executor = %q, want %q", string(got), string(want))
	}
}

func TestPluginZstdRoundTrip(t *testing.T) {
	t.Parallel()

	want := bytes.Repeat([]byte("wasm payload "), 64)
	compressed, err := CompressPluginModule(want)
	if err != nil {
		t.Fatalf("CompressPluginModule() returned error: %v", err)
	}
	got, err := DecompressPluginModule(compressed, assetmanifest.CompressionZstd)
	if err != nil {
		t.Fatalf("DecompressPluginModule() returned error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("decompressed plugin = %q, want %q", string(got), string(want))
	}
}

func TestDecompressPluginModuleRejectsUnsupportedCompression(t *testing.T) {
	t.Parallel()

	if _, err := DecompressPluginModule([]byte("payload"), "gzip"); err == nil {
		t.Fatal("DecompressPluginModule() should reject non-zstd compression")
	}
}
