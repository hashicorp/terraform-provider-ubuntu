//go:build windows || js || wasm

package runtime

import "os"

func flockExclusive(file *os.File) error {
	return nil
}
