// Copyright IBM Corp. 2026

// Package plugincodec encapsulates the compression schemes used for executor
// binaries and WASM plugin modules.
package plugincodec

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/assetmanifest"
	"github.com/klauspost/compress/zstd"
)

// CompressPluginModule zstd-encodes a WASM plugin module at the highest
// compression level.
func CompressPluginModule(data []byte) ([]byte, error) {
	encoder, err := zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(22)),
		zstd.WithEncoderCRC(false),
	)
	if err != nil {
		return nil, fmt.Errorf("create zstd encoder: %w", err)
	}
	defer encoder.Close()

	return encoder.EncodeAll(data, make([]byte, 0, len(data))), nil
}

// DecompressPluginModule decodes a WASM plugin module that was previously
// compressed with CompressPluginModule.
func DecompressPluginModule(data []byte, compression string) ([]byte, error) {
	switch compression {
	case assetmanifest.CompressionZstd:
		decoder, err := zstd.NewReader(nil)
		if err != nil {
			return nil, fmt.Errorf("create zstd decoder: %w", err)
		}
		defer decoder.Close()

		decoded, err := decoder.DecodeAll(data, nil)
		if err != nil {
			return nil, fmt.Errorf("decode zstd plugin module: %w", err)
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("unsupported plugin compression %q", compression)
	}
}

// CompressExecutorBinary gzip-encodes an executor binary at gzip.BestCompression.
// Executors are gzipped (not zstd) because the remote host is guaranteed to
// have gzip in its base toolchain, allowing the host to stream-decompress
// directly into the destination file.
func CompressExecutorBinary(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	writer, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("create gzip writer: %w", err)
	}
	if _, err := writer.Write(data); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("write gzip executor binary: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close gzip writer: %w", err)
	}
	return buf.Bytes(), nil
}

// DecompressExecutorBinary decodes an executor binary previously produced by
// CompressExecutorBinary. An empty compression label means the bytes are
// uncompressed and are returned as-is.
func DecompressExecutorBinary(data []byte, compression string) ([]byte, error) {
	switch compression {
	case "":
		return data, nil
	case assetmanifest.CompressionGzip:
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("create gzip reader: %w", err)
		}
		defer reader.Close()

		decoded, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("decode gzip executor binary: %w", err)
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("unsupported executor compression %q", compression)
	}
}
