// Copyright IBM Corp. 2026

package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-provider-ubuntu/executor/capabilities"
)

func TestPluginPoolAcquireHonorsCapacity(t *testing.T) {
	pool := newPluginPool([]*pluginInstance{{}, {}})

	first, err := pool.acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire first instance: %v", err)
	}
	second, err := pool.acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire second instance: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err = pool.acquire(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquire with exhausted pool error = %v, want deadline exceeded", err)
	}

	pool.release(first)

	third, err := pool.acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}

	pool.release(second)
	pool.release(third)
}

func TestPluginPoolAcquireLocksInstanceUntilRelease(t *testing.T) {
	pool := newPluginPool([]*pluginInstance{{}})

	instance, err := pool.acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire instance: %v", err)
	}

	locked := make(chan struct{}, 1)
	go func() {
		instance.mu.Lock()
		locked <- struct{}{}
		instance.mu.Unlock()
	}()

	select {
	case <-locked:
		t.Fatal("instance mutex unlocked before release")
	case <-time.After(20 * time.Millisecond):
	}

	pool.release(instance)

	select {
	case <-locked:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("instance mutex remained locked after release")
	}
}

func TestWASMRuntimeLoadPluginRejectsDuplicateNames(t *testing.T) {
	rt, err := NewWASMRuntime(context.Background(), capabilities.NewHostAPI(capabilities.HostProfile{}))
	if err != nil {
		t.Fatalf("NewWASMRuntime() returned error: %v", err)
	}
	defer func() {
		if closeErr := rt.Close(); closeErr != nil {
			t.Fatalf("Close() returned error: %v", closeErr)
		}
	}()

	wasmBytes := minimalPluginModule(t, true, false)
	if err := rt.LoadPlugin("linux_commands", wasmBytes); err != nil {
		t.Fatalf("LoadPlugin(first) returned error: %v", err)
	}

	err = rt.LoadPlugin("linux_commands", wasmBytes)
	if err == nil || err.Error() != `plugin "linux_commands" already loaded` {
		t.Fatalf("LoadPlugin(duplicate) error = %v, want already loaded error", err)
	}

	if got := len(rt.modules); got != 1 {
		t.Fatalf("loaded module count = %d, want 1", got)
	}
}

func TestWASMRuntimeLoadPluginFailsWhenImportIsUnresolved(t *testing.T) {
	rt, err := NewWASMRuntime(context.Background(), capabilities.NewHostAPI(capabilities.HostProfile{}))
	if err != nil {
		t.Fatalf("NewWASMRuntime() returned error: %v", err)
	}
	defer func() {
		if closeErr := rt.Close(); closeErr != nil {
			t.Fatalf("Close() returned error: %v", closeErr)
		}
	}()

	err = rt.LoadPlugin("missing_import", moduleWithMissingImport(t))
	if err == nil {
		t.Fatal("LoadPlugin() should fail when an import cannot be resolved")
	}
	if !strings.Contains(err.Error(), `instantiate plugin "missing_import" instance 0`) {
		t.Fatalf("LoadPlugin() error = %v, want instantiate failure for instance 0", err)
	}
	if !strings.Contains(err.Error(), `module[env] not instantiated`) {
		t.Fatalf("LoadPlugin() error = %v, want unresolved import detail", err)
	}
	if _, ok := rt.modules["missing_import"]; ok {
		t.Fatal("failed LoadPlugin() should not leave a loaded module entry")
	}
}

func TestWASMRuntimeLoadPluginFailsWhenInitializeTraps(t *testing.T) {
	rt, err := NewWASMRuntime(context.Background(), capabilities.NewHostAPI(capabilities.HostProfile{}))
	if err != nil {
		t.Fatalf("NewWASMRuntime() returned error: %v", err)
	}
	defer func() {
		if closeErr := rt.Close(); closeErr != nil {
			t.Fatalf("Close() returned error: %v", closeErr)
		}
	}()

	err = rt.LoadPlugin("trapping_init", minimalPluginModule(t, true, true))
	if err == nil {
		t.Fatal("LoadPlugin() should fail when _initialize traps")
	}
	if !strings.Contains(err.Error(), `instantiate plugin "trapping_init" instance 0`) {
		t.Fatalf("LoadPlugin() error = %v, want instantiate failure for instance 0", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unreachable") {
		t.Fatalf("LoadPlugin() error = %v, want unreachable trap detail", err)
	}
	if _, ok := rt.modules["trapping_init"]; ok {
		t.Fatal("failed LoadPlugin() should not leave a loaded module entry")
	}
}

func minimalPluginModule(t *testing.T, exportInitialize bool, trap bool) []byte {
	t.Helper()

	wasm := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

	wasm = append(wasm,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
		0x03, 0x02, 0x01, 0x00,
	)

	if exportInitialize {
		wasm = append(wasm,
			0x07, 0x0f, 0x01, 0x0b,
			'_', 'i', 'n', 'i', 't', 'i', 'a', 'l', 'i', 'z', 'e',
			0x00, 0x00,
		)
	}

	if trap {
		wasm = append(wasm, 0x0a, 0x05, 0x01, 0x03, 0x00, 0x00, 0x0b)
	} else {
		wasm = append(wasm, 0x0a, 0x04, 0x01, 0x02, 0x00, 0x0b)
	}

	return wasm
}

func moduleWithMissingImport(t *testing.T) []byte {
	t.Helper()

	wasm := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	wasm = append(wasm,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
		0x02, 0x0f, 0x01, 0x03, 'e', 'n', 'v', 0x07, 'm', 'i', 's', 's', 'i', 'n', 'g', 0x00, 0x00,
		0x03, 0x02, 0x01, 0x00,
		0x07, 0x0f, 0x01, 0x0b, '_', 'i', 'n', 'i', 't', 'i', 'a', 'l', 'i', 'z', 'e', 0x00, 0x01,
		0x0a, 0x04, 0x01, 0x02, 0x00, 0x0b,
	)
	return wasm
}
