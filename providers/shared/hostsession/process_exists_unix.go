//go:build !windows && !js && !wasm

package hostsession

import "syscall"

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return err != syscall.ESRCH
}
