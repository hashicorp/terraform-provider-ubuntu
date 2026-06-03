// Copyright IBM Corp. 2026

package digest

import (
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/zeebo/blake3"
	"github.com/zeebo/xxh3"
	"golang.org/x/crypto/sha3"
)

const (
	AlgorithmBlake3   = "blake3"
	AlgorithmShake256 = "shake256"
	AlgorithmXXH3_128 = "xxh3_128"
)

const shake256DigestBytes = 64

func DigestBytes(algorithm string, data []byte) (string, error) {
	switch algorithm {
	case AlgorithmBlake3:
		sum := blake3.Sum256(data)
		return algorithm + ":" + fmt.Sprintf("%x", sum[:]), nil
	case AlgorithmShake256:
		digester := sha3.NewShake256()
		if _, err := digester.Write(data); err != nil {
			return "", fmt.Errorf("write shake256 digest input: %w", err)
		}
		sum := make([]byte, shake256DigestBytes)
		if _, err := digester.Read(sum); err != nil {
			return "", fmt.Errorf("read shake256 digest output: %w", err)
		}
		return algorithm + ":" + fmt.Sprintf("%x", sum), nil
	case AlgorithmXXH3_128:
		sum := xxh3.Hash128(data)
		bytes := sum.Bytes()
		return algorithm + ":" + fmt.Sprintf("%x", bytes[:]), nil
	default:
		return "", fmt.Errorf("unsupported digest algorithm %q", algorithm)
	}
}

func DigestReader(algorithm string, reader io.Reader) (string, error) {
	switch algorithm {
	case AlgorithmBlake3:
		digester := blake3.New()
		if _, err := io.Copy(digester, reader); err != nil {
			return "", fmt.Errorf("read blake3 digest input: %w", err)
		}
		return algorithm + ":" + fmt.Sprintf("%x", digester.Sum(nil)), nil
	case AlgorithmShake256:
		digester := sha3.NewShake256()
		if _, err := io.Copy(digester, reader); err != nil {
			return "", fmt.Errorf("read shake256 digest input: %w", err)
		}
		sum := make([]byte, shake256DigestBytes)
		if _, err := digester.Read(sum); err != nil {
			return "", fmt.Errorf("read shake256 digest output: %w", err)
		}
		return algorithm + ":" + fmt.Sprintf("%x", sum), nil
	case AlgorithmXXH3_128:
		digester := xxh3.New128()
		if _, err := io.Copy(digester, reader); err != nil {
			return "", fmt.Errorf("read xxh3_128 digest input: %w", err)
		}
		sum := digester.Sum128()
		bytes := sum.Bytes()
		return algorithm + ":" + fmt.Sprintf("%x", bytes[:]), nil
	default:
		return "", fmt.Errorf("unsupported digest algorithm %q", algorithm)
	}
}

func MustDigestBytes(algorithm string, data []byte) string {
	digest, err := DigestBytes(algorithm, data)
	if err != nil {
		panic(err)
	}
	return digest
}

func VerifyDigest(data []byte, want string) error {
	algorithm, err := Algorithm(want)
	if err != nil {
		return err
	}
	got, err := DigestBytes(algorithm, data)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("%s digest mismatch: expected %s, got %s", algorithm, want, got)
	}
	return nil
}

func Algorithm(value string) (string, error) {
	algorithm, encoded, ok := strings.Cut(strings.TrimSpace(value), ":")
	if !ok || algorithm == "" || encoded == "" {
		return "", fmt.Errorf("invalid digest %q", value)
	}
	expectedHexLength, err := expectedDigestHexLength(algorithm)
	if err != nil {
		return "", err
	}
	if len(encoded) != expectedHexLength {
		return "", fmt.Errorf("invalid %s digest %q", algorithm, value)
	}
	if _, err := hex.DecodeString(encoded); err != nil {
		return "", fmt.Errorf("invalid %s digest %q", algorithm, value)
	}
	return algorithm, nil
}

func Token(value string) string {
	return strings.NewReplacer(":", "_").Replace(strings.TrimSpace(value))
}

func expectedDigestHexLength(algorithm string) (int, error) {
	switch algorithm {
	case AlgorithmBlake3:
		return 64, nil
	case AlgorithmShake256:
		return shake256DigestBytes * 2, nil
	case AlgorithmXXH3_128:
		return 32, nil
	default:
		return 0, fmt.Errorf("unsupported digest algorithm %q", algorithm)
	}
}
