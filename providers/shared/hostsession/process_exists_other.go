// Copyright IBM Corp. 2026

//go:build windows || js || wasm

package hostsession

func processExists(pid int) bool {
	return pid > 0
}
