//go:build !windows && !js && !wasm

package runtime

import (
	"os"
	"syscall"
)

func flockExclusive(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
}
