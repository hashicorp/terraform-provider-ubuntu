package assets

import (
	"fmt"

	"github.com/klauspost/compress/zstd"
)

const CompressionZstd = "zstd"

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

func DecompressPluginModule(data []byte, compression string) ([]byte, error) {
	switch compression {
	case CompressionZstd:
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
