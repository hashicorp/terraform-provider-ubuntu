//go:build linux

package capabilities

import (
	"runtime"
	"syscall"
)

func discoverKernel(p *HostProfile) {
	var utsname syscall.Utsname
	if err := syscall.Uname(&utsname); err != nil {
		p.Arch = runtime.GOARCH
		return
	}
	p.Kernel = utsInt8ToString(utsname.Sysname[:])
	p.KernelVersion = utsInt8ToString(utsname.Release[:])
	p.Arch = runtime.GOARCH
	machine := utsInt8ToString(utsname.Machine[:])
	if machine != "" {
		p.Arch = machine
	}
}

func utsInt8ToString(buf []int8) string {
	b := make([]byte, 0, len(buf))
	for _, v := range buf {
		if v == 0 {
			break
		}
		b = append(b, byte(v))
	}
	return string(b)
}
