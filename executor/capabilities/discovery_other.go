// Copyright IBM Corp. 2026

//go:build !linux

package capabilities

import "runtime"

func discoverKernel(p *HostProfile) {
	p.Arch = runtime.GOARCH
	p.Kernel = runtime.GOOS
}
