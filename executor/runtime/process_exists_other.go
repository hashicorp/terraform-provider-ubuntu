// Copyright IBM Corp. 2026

//go:build windows || js || wasm

package runtime

func processExists(pid int) bool {
	return pid > 0
}
