// Copyright IBM Corp. 2026

package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/terraform-provider-ubuntu/executor/capabilities"
	"github.com/hashicorp/terraform-provider-ubuntu/executor/logging"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

const (
	// Validation paths should not need large host callback payloads, and TinyGo
	// guest heaps on remote agents are sensitive to oversized eager allocations.
	validateHostResponseBufferSize = 64 << 10
	defaultHostResponseBufferSize  = 256 << 10
	pluginInstancePoolSize         = 4
)

type guestBuffer struct {
	ptr uint32
	cap uint32
}

type pluginInstance struct {
	mod api.Module
	mu  sync.Mutex
}

type pluginPool struct {
	instances []*pluginInstance
	available chan *pluginInstance
}

func newPluginPool(instances []*pluginInstance) *pluginPool {
	available := make(chan *pluginInstance, len(instances))
	for _, instance := range instances {
		available <- instance
	}

	return &pluginPool{
		instances: instances,
		available: available,
	}
}

func (p *pluginPool) acquire(ctx context.Context) (*pluginInstance, error) {
	select {
	case instance, ok := <-p.available:
		if !ok {
			return nil, fmt.Errorf("plugin pool is closed")
		}
		instance.mu.Lock()
		return instance, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *pluginPool) release(instance *pluginInstance) {
	instance.mu.Unlock()
	p.available <- instance
}

// WASMRuntime manages the wazero runtime and loaded plugin modules.
type WASMRuntime struct {
	engine  wazero.Runtime
	hostAPI *capabilities.HostAPI
	ctx     context.Context

	mu              sync.Mutex
	modules         map[string]*pluginPool
	callbackBuffers map[string]guestBuffer
	traceSeq        uint64
}

// NewWASMRuntime creates a new runtime with host functions registered.
func NewWASMRuntime(ctx context.Context, hostAPI *capabilities.HostAPI) (*WASMRuntime, error) {
	engine := wazero.NewRuntime(ctx)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, engine); err != nil {
		engine.Close(ctx)
		return nil, fmt.Errorf("instantiate wasi: %w", err)
	}

	rt := &WASMRuntime{
		engine:          engine,
		hostAPI:         hostAPI,
		ctx:             ctx,
		modules:         make(map[string]*pluginPool),
		callbackBuffers: make(map[string]guestBuffer),
	}

	if err := rt.registerHostFunctions(ctx); err != nil {
		engine.Close(ctx)
		return nil, fmt.Errorf("register host functions: %w", err)
	}

	return rt, nil
}

// registerHostFunctions binds Go functions as WASM imports under the "host" module.
//
// ABI convention: all host functions accept a pointer and length into WASM linear
// memory for input (JSON string), and return a pointer+length packed into a single
// uint64 (ptr << 32 | len) for the response. The response is written directly into
// grown guest memory to avoid re-entering TinyGo exports during a host callback.
//
// For simplicity, each host function takes (ptr uint32, len uint32) -> uint64.
func (rt *WASMRuntime) registerHostFunctions(ctx context.Context) error {
	_, err := rt.engine.NewHostModuleBuilder("host").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) uint64 {
			return rt.handleHostCall(ctx, mod, ptr, size, "file_read")
		}).
		Export("file_read").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) uint64 {
			return rt.handleHostCall(ctx, mod, ptr, size, "file_write")
		}).
		Export("file_write").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) uint64 {
			return rt.handleHostCall(ctx, mod, ptr, size, "dir_ensure")
		}).
		Export("dir_ensure").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) uint64 {
			return rt.handleHostCall(ctx, mod, ptr, size, "dir_read")
		}).
		Export("dir_read").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) uint64 {
			return rt.handleHostCall(ctx, mod, ptr, size, "file_delete")
		}).
		Export("file_delete").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) uint64 {
			return rt.handleHostCall(ctx, mod, ptr, size, "file_exists")
		}).
		Export("file_exists").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) uint64 {
			return rt.handleHostCall(ctx, mod, ptr, size, "file_stat")
		}).
		Export("file_stat").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) uint64 {
			return rt.handleHostCall(ctx, mod, ptr, size, "file_chown")
		}).
		Export("file_chown").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) uint64 {
			return rt.handleHostCall(ctx, mod, ptr, size, "file_chmod")
		}).
		Export("file_chmod").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) uint64 {
			return rt.handleHostCall(ctx, mod, ptr, size, "file_lock")
		}).
		Export("file_lock").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) uint64 {
			return rt.handleHostCall(ctx, mod, ptr, size, "file_unlock")
		}).
		Export("file_unlock").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) uint64 {
			return rt.handleHostCall(ctx, mod, ptr, size, "file_rename")
		}).
		Export("file_rename").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) uint64 {
			return rt.handleHostCall(ctx, mod, ptr, size, "file_readlink")
		}).
		Export("file_readlink").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) uint64 {
			return rt.handleHostCall(ctx, mod, ptr, size, "file_symlink")
		}).
		Export("file_symlink").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) uint64 {
			return rt.handleHostCall(ctx, mod, ptr, size, "identity_lookup_user")
		}).
		Export("identity_lookup_user").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) uint64 {
			return rt.handleHostCall(ctx, mod, ptr, size, "identity_lookup_group")
		}).
		Export("identity_lookup_group").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) uint64 {
			return rt.handleHostCall(ctx, mod, ptr, size, "cmd_exec")
		}).
		Export("cmd_exec").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) uint64 {
			return rt.handleHostCall(ctx, mod, ptr, size, "cmd_exists")
		}).
		Export("cmd_exists").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) uint64 {
			return rt.handleHostCall(ctx, mod, ptr, size, "host_profile")
		}).
		Export("host_profile").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) uint64 {
			return rt.handleHostCall(ctx, mod, ptr, size, "fetch_url")
		}).
		Export("fetch_url").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) uint64 {
			return rt.handleHostCall(ctx, mod, ptr, size, "log_info")
		}).
		Export("log_info").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, ptr, size uint32) uint64 {
			return rt.handleHostCall(ctx, mod, ptr, size, "log_warn")
		}).
		Export("log_warn").
		Instantiate(ctx)

	return err
}

// hostCallRequest is the generic JSON envelope read from WASM memory.
type hostCallRequest struct {
	Path          string   `json:"path,omitempty"`
	Target        string   `json:"target,omitempty"`
	URL           string   `json:"url,omitempty"`
	Content       string   `json:"content,omitempty"`
	ContentBase64 string   `json:"content_base64,omitempty"`
	Mode          uint32   `json:"mode,omitempty"`
	UID           int      `json:"uid,omitempty"`
	GID           int      `json:"gid,omitempty"`
	Owner         string   `json:"owner,omitempty"`
	Group         string   `json:"group,omitempty"`
	Name          string   `json:"name,omitempty"`
	Args          []string `json:"args,omitempty"`
	Separator     string   `json:"separator,omitempty"`
	Delimiter     string   `json:"delimiter,omitempty"`
	Message       string   `json:"message,omitempty"`
}

// hostCallResponse is written back to WASM memory.
type hostCallResponse struct {
	Result interface{} `json:"result,omitempty"`
	Error  string      `json:"error,omitempty"`
}

// handleHostCall reads a JSON request from WASM memory, dispatches to the
// appropriate HostAPI method, and writes the JSON response back.
func (rt *WASMRuntime) handleHostCall(ctx context.Context, mod api.Module, ptr, size uint32, funcName string) uint64 {
	traceID := rt.nextTraceID()
	started := time.Now()
	// Read input from WASM memory.
	input, ok := mod.Memory().Read(ptr, size)
	if !ok {
		log.Printf("[hostcall#%d] module=%s func=%s error=failed to read wasm memory", traceID, mod.Name(), funcName)
		return rt.writeResponse(mod, hostCallResponse{Error: "failed to read WASM memory"})
	}

	var req hostCallRequest
	if err := json.Unmarshal(input, &req); err != nil {
		log.Printf("[hostcall#%d] module=%s func=%s invalid_request duration=%s preview=%s err=%v", traceID, mod.Name(), funcName, time.Since(started), logging.Preview(string(input), 200), err)
		return rt.writeResponse(mod, hostCallResponse{Error: fmt.Sprintf("invalid request JSON: %v", err)})
	}

	log.Printf("[hostcall#%d] module=%s func=%s start request=%s", traceID, mod.Name(), funcName, logging.SummarizeJSON(input))

	var resp hostCallResponse

	switch funcName {
	case "file_read":
		content, err := rt.hostAPI.FileReadWithContext(ctx, req.Path)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Result = content
		}

	case "file_write":
		content := req.Content
		if req.ContentBase64 != "" {
			decoded, decodeErr := base64.StdEncoding.DecodeString(req.ContentBase64)
			if decodeErr != nil {
				resp.Error = decodeErr.Error()
				break
			}
			content = string(decoded)
		}
		err := rt.hostAPI.FileWrite(ctx, req.Path, content, 0o644)
		if req.Mode != 0 {
			err = rt.hostAPI.FileWrite(ctx, req.Path, content, os.FileMode(req.Mode))
		}
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Result = true
		}

	case "dir_ensure":
		err := rt.hostAPI.DirEnsure(ctx, req.Path, os.FileMode(req.Mode))
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Result = true
		}

	case "dir_read":
		entries, err := rt.hostAPI.ReadDir(ctx, req.Path)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Result = entries
		}

	case "file_delete":
		err := rt.hostAPI.FileDelete(ctx, req.Path)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Result = true
		}

	case "file_exists":
		resp.Result = rt.hostAPI.FileExists(req.Path)

	case "file_stat":
		stat, err := rt.hostAPI.FileStatWithContext(ctx, req.Path)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Result = stat
		}

	case "file_chown":
		var err error
		if req.Owner != "" || req.Group != "" {
			err = rt.hostAPI.FileChownNames(ctx, req.Path, req.Owner, req.Group)
		} else {
			err = rt.hostAPI.FileChown(ctx, req.Path, req.UID, req.GID)
		}
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Result = true
		}

	case "file_chmod":
		err := rt.hostAPI.FileChmod(ctx, req.Path, os.FileMode(req.Mode))
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Result = true
		}

	case "file_lock":
		err := rt.hostAPI.FileLock(ctx, req.Path)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Result = true
		}

	case "file_unlock":
		err := rt.hostAPI.FileUnlock(req.Path)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Result = true
		}

	case "file_rename":
		err := rt.hostAPI.FileRename(ctx, req.Path, req.Target)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Result = true
		}

	case "file_readlink":
		target, err := rt.hostAPI.FileReadlink(ctx, req.Path)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Result = target
		}

	case "file_symlink":
		err := rt.hostAPI.FileSymlink(ctx, req.Target, req.Path)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Result = true
		}

	case "identity_lookup_user":
		identity, err := rt.hostAPI.LookupUser(req.Name)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Result = identity
		}

	case "identity_lookup_group":
		identity, err := rt.hostAPI.LookupGroup(req.Name)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Result = identity
		}

	case "cmd_exec":
		result := rt.hostAPI.CmdExec(ctx, req.Name, req.Args...)
		resp.Result = result

	case "cmd_exists":
		resp.Result = rt.hostAPI.CmdExists(req.Name)

	case "host_profile":
		r, err := rt.hostAPI.HostProfileJSON()
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Result = json.RawMessage(r)
		}

	case "fetch_url":
		content, err := rt.hostAPI.FetchURL(ctx, req.URL)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Result = base64.StdEncoding.EncodeToString(content)
		}

	case "log_info":
		rt.hostAPI.LogInfo(req.Message)
		resp.Result = true

	case "log_warn":
		rt.hostAPI.LogWarn(req.Message)
		resp.Result = true

	default:
		resp.Error = fmt.Sprintf("unknown host function: %s", funcName)
	}

	if resp.Error != "" {
		log.Printf("[hostcall#%d] module=%s func=%s error duration=%s err=%s", traceID, mod.Name(), funcName, time.Since(started), resp.Error)
	} else {
		resultSummary := "<nil>"
		if resp.Result != nil {
			if data, err := json.Marshal(resp.Result); err == nil {
				resultSummary = logging.SummarizeJSON(data)
			}
		}
		log.Printf("[hostcall#%d] module=%s func=%s complete duration=%s result=%s", traceID, mod.Name(), funcName, time.Since(started), resultSummary)
	}

	return rt.writeResponse(mod, resp)
}

// writeResponse serializes a response and writes it into guest memory,
// returning ptr<<32|len.
func (rt *WASMRuntime) writeResponse(mod api.Module, resp hostCallResponse) uint64 {
	data, err := json.Marshal(resp)
	if err != nil {
		// Last resort: write a bare error.
		data = []byte(`{"error":"marshal failed"}`)
	}

	rt.mu.Lock()
	buffer, ok := rt.callbackBuffers[mod.Name()]
	rt.mu.Unlock()
	if !ok || uint32(len(data)) > buffer.cap {
		return 0
	}

	if !mod.Memory().Write(buffer.ptr, data) {
		return 0
	}

	return uint64(buffer.ptr)<<32 | uint64(len(data))
}

// LoadPlugin compiles and instantiates a WASM module as a named plugin.
func (rt *WASMRuntime) LoadPlugin(name string, wasmBytes []byte) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if _, ok := rt.modules[name]; ok {
		return fmt.Errorf("plugin %q already loaded", name)
	}

	log.Printf("[wasm] load start plugin=%q bytes=%d", name, len(wasmBytes))

	compiled, err := rt.engine.CompileModule(rt.ctx, wasmBytes)
	if err != nil {
		return fmt.Errorf("compile plugin %q: %w", name, err)
	}

	mod, err := rt.engine.InstantiateModule(
		rt.ctx,
		compiled,
		wazero.NewModuleConfig().WithName(fmt.Sprintf("%s[%d]", name, 0)).WithStartFunctions("_initialize").WithStdout(io.Discard).WithStderr(os.Stderr),
	)
	if err != nil {
		return fmt.Errorf("instantiate plugin %q instance 0: %w", name, err)
	}

	instances := []*pluginInstance{{mod: mod}}
	for i := 1; i < pluginInstancePoolSize; i++ {
		instanceMod, instanceErr := rt.engine.InstantiateModule(
			rt.ctx,
			compiled,
			wazero.NewModuleConfig().WithName(fmt.Sprintf("%s[%d]", name, i)).WithStartFunctions("_initialize").WithStdout(io.Discard).WithStderr(os.Stderr),
		)
		if instanceErr != nil {
			for _, instance := range instances {
				_ = instance.mod.Close(rt.ctx)
			}
			return fmt.Errorf("instantiate plugin %q instance %d: %w", name, i, instanceErr)
		}
		instances = append(instances, &pluginInstance{mod: instanceMod})
	}

	rt.modules[name] = newPluginPool(instances)
	log.Printf("[wasm] load complete plugin=%q instances=%d", name, len(instances))
	return nil
}

// callPluginFunc invokes an exported function on a plugin. The function
// receives a JSON-encoded argument string and returns a JSON-encoded result.
// ABI: func(ptr, len) -> ptr_len_packed (uint64).
func (rt *WASMRuntime) callPluginFunc(ctx context.Context, name, funcName string, arg interface{}) (json.RawMessage, error) {
	if ctx == nil {
		ctx = rt.ctx
	}

	traceID := rt.nextTraceID()
	started := time.Now()
	argSummary := summarizePluginArg(arg)

	rt.mu.Lock()
	pool, ok := rt.modules[name]
	rt.mu.Unlock()
	if !ok {
		log.Printf("[wasm#%d] plugin=%q func=%q error duration=%s err=plugin not loaded arg=%s", traceID, name, funcName, time.Since(started), argSummary)
		return nil, fmt.Errorf("plugin %q not loaded", name)
	}

	instance, err := pool.acquire(ctx)
	if err != nil {
		log.Printf("[wasm#%d] plugin=%q func=%q error duration=%s err=%v arg=%s", traceID, name, funcName, time.Since(started), err, argSummary)
		return nil, fmt.Errorf("acquire plugin %q instance: %w", name, err)
	}
	defer pool.release(instance)

	mod := instance.mod
	log.Printf("[wasm#%d] plugin=%q func=%q start instance=%s arg=%s", traceID, name, funcName, mod.Name(), argSummary)

	fn := mod.ExportedFunction(funcName)
	if fn == nil {
		log.Printf("[wasm#%d] plugin=%q func=%q error duration=%s instance=%s err=no export", traceID, name, funcName, time.Since(started), mod.Name())
		return nil, fmt.Errorf("plugin %q has no export %q", name, funcName)
	}

	argData, err := json.Marshal(arg)
	if err != nil {
		log.Printf("[wasm#%d] plugin=%q func=%q error duration=%s instance=%s err=marshal arg: %v", traceID, name, funcName, time.Since(started), mod.Name(), err)
		return nil, fmt.Errorf("marshal arg: %w", err)
	}

	argPtr, err := allocateGuestBuffer(ctx, mod, uint32(len(argData)))
	if err != nil {
		log.Printf("[wasm#%d] plugin=%q func=%q error duration=%s instance=%s err=allocate arg: %v", traceID, name, funcName, time.Since(started), mod.Name(), err)
		return nil, fmt.Errorf("allocate arg for plugin %q: %w", name, err)
	}
	defer freeGuestBuffer(ctx, mod, argPtr)
	if len(argData) > 0 && !mod.Memory().Write(argPtr, argData) {
		return nil, fmt.Errorf("write arg for plugin %q", name)
	}

	responseCap := hostResponseBufferSize(funcName)
	responsePtr, err := allocateGuestBuffer(ctx, mod, responseCap)
	if err != nil {
		log.Printf("[wasm#%d] plugin=%q func=%q error duration=%s instance=%s err=allocate callback buffer: %v", traceID, name, funcName, time.Since(started), mod.Name(), err)
		return nil, fmt.Errorf("allocate host callback buffer for plugin %q: %w", name, err)
	}
	defer freeGuestBuffer(ctx, mod, responsePtr)

	callbackBuffer := guestBuffer{ptr: responsePtr, cap: responseCap}

	rt.mu.Lock()
	rt.callbackBuffers[mod.Name()] = callbackBuffer
	rt.mu.Unlock()
	defer func() {
		rt.mu.Lock()
		delete(rt.callbackBuffers, mod.Name())
		rt.mu.Unlock()
	}()

	// Call the plugin function.
	results, err := fn.Call(ctx, uint64(argPtr), uint64(len(argData)))
	if err != nil {
		log.Printf("[wasm#%d] plugin=%q func=%q error duration=%s instance=%s err=%v", traceID, name, funcName, time.Since(started), mod.Name(), err)
		return nil, fmt.Errorf("call %s.%s: %w", name, funcName, err)
	}
	if len(results) == 0 {
		log.Printf("[wasm#%d] plugin=%q func=%q error duration=%s instance=%s err=no results", traceID, name, funcName, time.Since(started), mod.Name())
		return nil, fmt.Errorf("%s.%s returned no results", name, funcName)
	}

	packed := results[0]
	resPtr := uint32(packed >> 32)
	resLen := uint32(packed & 0xFFFFFFFF)

	if resLen == 0 {
		log.Printf("[wasm#%d] plugin=%q func=%q complete duration=%s instance=%s result=null", traceID, name, funcName, time.Since(started), mod.Name())
		return json.RawMessage("null"), nil
	}

	resData, ok := mod.Memory().Read(resPtr, resLen)
	if !ok {
		log.Printf("[wasm#%d] plugin=%q func=%q error duration=%s instance=%s err=failed to read result memory", traceID, name, funcName, time.Since(started), mod.Name())
		return nil, fmt.Errorf("failed to read result from plugin %q memory", name)
	}
	result := append(json.RawMessage(nil), resData...)
	if err := freeGuestBuffer(ctx, mod, resPtr); err != nil {
		log.Printf("[wasm#%d] plugin=%q func=%q error duration=%s instance=%s err=free result: %v", traceID, name, funcName, time.Since(started), mod.Name(), err)
		return nil, fmt.Errorf("free result for plugin %q: %w", name, err)
	}

	log.Printf("[wasm#%d] plugin=%q func=%q complete duration=%s instance=%s result=%s", traceID, name, funcName, time.Since(started), mod.Name(), logging.SummarizeJSON(result))
	return result, nil
}

func hostResponseBufferSize(funcName string) uint32 {
	switch funcName {
	case "validate":
		return validateHostResponseBufferSize
	default:
		return defaultHostResponseBufferSize
	}
}

func allocateGuestBuffer(ctx context.Context, mod api.Module, size uint32) (uint32, error) {
	if size == 0 {
		return 0, nil
	}

	allocFn := mod.ExportedFunction("alloc")
	if allocFn == nil {
		return 0, fmt.Errorf("module %q has no alloc export", mod.Name())
	}

	results, err := allocFn.Call(ctx, uint64(size))
	if err != nil {
		return 0, fmt.Errorf("call alloc(%d): %w", size, err)
	}
	if len(results) == 0 {
		return 0, fmt.Errorf("alloc(%d) returned no result", size)
	}

	ptr := uint32(results[0])
	if ptr == 0 {
		return 0, fmt.Errorf("alloc(%d) returned null", size)
	}

	return ptr, nil
}

func freeGuestBuffer(ctx context.Context, mod api.Module, ptr uint32) error {
	if ptr == 0 {
		return nil
	}

	freeFn := mod.ExportedFunction("tf_linux_provider_free")
	if freeFn == nil {
		freeFn = mod.ExportedFunction("free")
	}
	if freeFn == nil {
		return nil
	}

	if _, err := freeFn.Call(ctx, uint64(ptr)); err != nil {
		return fmt.Errorf("call free(%d): %w", ptr, err)
	}

	return nil
}

// CallSchema invokes the "schema" export on a plugin.
func (rt *WASMRuntime) CallSchema(name string) (json.RawMessage, error) {
	return rt.callPluginFunc(rt.ctx, name, "schema", nil)
}

// CallValidate invokes the "validate" export on a plugin.
func (rt *WASMRuntime) CallValidate(ctx context.Context, name string, config json.RawMessage) (json.RawMessage, error) {
	return rt.callPluginFunc(ctx, name, "validate", config)
}

// CallRead invokes the "read" export on a plugin.
func (rt *WASMRuntime) CallRead(ctx context.Context, name string, state json.RawMessage) (json.RawMessage, error) {
	return rt.callPluginFunc(ctx, name, "read", state)
}

// CallCreate invokes the "create" export on a plugin.
func (rt *WASMRuntime) CallCreate(ctx context.Context, name string, plan json.RawMessage) (json.RawMessage, error) {
	return rt.callPluginFunc(ctx, name, "create", plan)
}

// CallUpdate invokes the "update" export on a plugin.
func (rt *WASMRuntime) CallUpdate(ctx context.Context, name string, plan json.RawMessage) (json.RawMessage, error) {
	return rt.callPluginFunc(ctx, name, "update", plan)
}

// CallDelete invokes the "delete" export on a plugin.
func (rt *WASMRuntime) CallDelete(ctx context.Context, name string, state json.RawMessage) (json.RawMessage, error) {
	return rt.callPluginFunc(ctx, name, "delete", state)
}

// CallImport invokes the "import" export on a plugin.
func (rt *WASMRuntime) CallImport(ctx context.Context, name string, importID json.RawMessage) (json.RawMessage, error) {
	return rt.callPluginFunc(ctx, name, "import", importID)
}

// CallDataRead invokes the "data_read" export on a plugin.
func (rt *WASMRuntime) CallDataRead(ctx context.Context, name string, config json.RawMessage) (json.RawMessage, error) {
	return rt.callPluginFunc(ctx, name, "data_read", config)
}

// CallInvoke invokes the "invoke" export on a plugin.
func (rt *WASMRuntime) CallInvoke(ctx context.Context, name string, config json.RawMessage) (json.RawMessage, error) {
	return rt.callPluginFunc(ctx, name, "invoke", config)
}

// GetModule returns a loaded module by name, or nil.
func (rt *WASMRuntime) GetModule(name string) api.Module {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	pool := rt.modules[name]
	if pool == nil || len(pool.instances) == 0 {
		return nil
	}
	return pool.instances[0].mod
}

// Close shuts down the runtime and all loaded modules.
func (rt *WASMRuntime) Close() error {
	return rt.engine.Close(rt.ctx)
}

func (rt *WASMRuntime) nextTraceID() uint64 {
	return atomic.AddUint64(&rt.traceSeq, 1)
}

func summarizePluginArg(arg interface{}) string {
	switch typed := arg.(type) {
	case nil:
		return "<nil>"
	case json.RawMessage:
		return logging.SummarizeJSON(typed)
	case []byte:
		return logging.SummarizeJSON(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprintf("<marshal-error %v>", err)
		}
		return logging.SummarizeJSON(data)
	}
}
