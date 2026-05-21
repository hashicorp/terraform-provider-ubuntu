package pluginsdk

import (
	"encoding/json"
	"fmt"
	"os"
)

// ---------------------------------------------------------------------------
// Host function stubs
//
// These are the Go-level APIs that plugin authors call. Each one delegates to
// a package-level function variable so the executor can wire them at runtime
// (via wazero host function injection). The defaults return typed runtime
// boundary errors so accidental calls outside a proper host fail diagnostically
// instead of crashing the plugin process.
// ---------------------------------------------------------------------------

func missingHostCapability(capability, operation string) error {
	return &HostCapabilityError{
		Capability: capability,
		Operation:  operation,
		Kind:       HostCapabilityErrorUnsupportedHost,
		Detail:     "the executor host did not wire this capability for the current plugin invocation",
	}
}

func missingHostLogging(level, msg string) {
	_, _ = fmt.Fprintf(os.Stderr, "pluginsdk: missing host logging capability %q; dropping message: %s\n", level, msg)
}

var (
	// File operations
	FileRead  = func(path string) ([]byte, error) { return nil, missingHostCapability("file_read", "FileRead") }
	FileWrite = func(path string, data []byte, mode uint32) error {
		return missingHostCapability("file_write", "FileWrite")
	}
	DirEnsure    = func(path string, mode uint32) error { return missingHostCapability("dir_ensure", "DirEnsure") }
	ReadDir      = func(path string) ([]DirEntry, error) { return nil, missingHostCapability("dir_read", "ReadDir") }
	FileDelete   = func(path string) error { return missingHostCapability("file_delete", "FileDelete") }
	FileExists   = func(path string) (bool, error) { return false, missingHostCapability("file_exists", "FileExists") }
	FileStat_    = func(path string) (*FileStat, error) { return nil, missingHostCapability("file_stat", "FileStat") }
	FileChown    = func(path string, owner, group string) error { return missingHostCapability("file_chown", "FileChown") }
	FileChmod    = func(path string, mode uint32) error { return missingHostCapability("file_chmod", "FileChmod") }
	FileLock     = func(path string) (uint32, error) { return 0, missingHostCapability("file_lock", "FileLock") }
	FileUnlock   = func(handle uint32) error { return missingHostCapability("file_unlock", "FileUnlock") }
	FileRename   = func(from, to string) error { return missingHostCapability("file_rename", "FileRename") }
	FileReadlink = func(path string) (string, error) {
		return "", missingHostCapability("file_readlink", "FileReadlink")
	}
	FileSymlink = func(target, path string) error { return missingHostCapability("file_symlink", "FileSymlink") }
	FetchURL    = func(url string) ([]byte, error) { return nil, missingHostCapability("fetch_url", "FetchURL") }

	// Command execution
	CmdExec = func(cmd string, args []string) (*CmdResult, error) {
		return nil, missingHostCapability("cmd_exec", "CmdExec")
	}
	CmdExists  = func(name string) (bool, error) { return false, missingHostCapability("cmd_exists", "CmdExists") }
	LookupUser = func(name string) (*IdentityUser, error) {
		return nil, missingHostCapability("identity_lookup_user", "LookupUser")
	}
	LookupGroup = func(name string) (*IdentityGroup, error) {
		return nil, missingHostCapability("identity_lookup_group", "LookupGroup")
	}

	// Host introspection
	GetHostProfile    = func() (*HostProfile, error) { return nil, missingHostCapability("host_profile", "GetHostProfile") }
	GetDistroFamily   = defaultGetDistroFamily
	HasCommand        = defaultHasCommand
	GetPackageManager = defaultGetPackageManager

	// Logging -- fire-and-forget, no return value
	LogInfo  = func(msg string) { missingHostLogging("log_info", msg) }
	LogWarn  = func(msg string) { missingHostLogging("log_warn", msg) }
	LogError = func(msg string) { missingHostLogging("log_error", msg) }
	LogDebug = func(msg string) { missingHostLogging("log_debug", msg) }
	LogTrace = func(msg string) { missingHostLogging("log_trace", msg) }
)

// ---------------------------------------------------------------------------
// Plugin registry
// ---------------------------------------------------------------------------

var (
	resources   = map[string]Resource{}
	dataSources = map[string]DataSource{}
	actions     = map[string]Action{}

	currentProviderName string
)

// ProviderName returns the Terraform provider name for the active request.
func ProviderName() string {
	return currentProviderName
}

// RegisterResource registers a managed resource with the SDK.
// Call this from an init() function in your plugin.
func RegisterResource(r Resource) {
	name := r.Name()
	if _, exists := resources[name]; exists {
		panic(fmt.Sprintf("pluginsdk: resource %q already registered", name))
	}
	resources[name] = r
}

// RegisterDataSource registers a read-only data source with the SDK.
// Call this from an init() function in your plugin.
func RegisterDataSource(d DataSource) {
	name := d.Name()
	if _, exists := dataSources[name]; exists {
		panic(fmt.Sprintf("pluginsdk: data source %q already registered", name))
	}
	dataSources[name] = d
}

// RegisterAction registers an imperative action with the SDK.
func RegisterAction(a Action) {
	name := a.Name()
	if _, exists := actions[name]; exists {
		panic(fmt.Sprintf("pluginsdk: action %q already registered", name))
	}
	actions[name] = a
}

// ---------------------------------------------------------------------------
// Main dispatch loop
//
// The executor launches the WASM module, writes a JSON Request to stdin, and
// reads a JSON Response from stdout. This function handles that protocol.
// Plugins call Run() from their main() function.
// ---------------------------------------------------------------------------

// Run is the entry point for a plugin. It reads a single request from stdin,
// dispatches it to the appropriate registered handler, and writes the response
// to stdout. The process then exits.
//
// A typical plugin main():
//
//	func main() {
//	    pluginsdk.RegisterResource(&myResource{})
//	    pluginsdk.Run()
//	}
func Run() {
	var req Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		writeError(fmt.Sprintf("failed to decode request: %v", err))
		return
	}

	resp := dispatch(req)
	writeResponse(resp)
}

func dispatch(req Request) Response {
	previousProviderName := currentProviderName
	currentProviderName = req.ProviderName
	defer func() { currentProviderName = previousProviderName }()

	switch req.Operation {
	case "get_schema":
		return handleGetSchema(req)
	case "describe":
		return handleDescribe()
	case "validate":
		return handleValidate(req)
	case "read":
		return handleRead(req)
	case "create":
		return handleCreate(req)
	case "update":
		return handleUpdate(req)
	case "delete":
		return handleDelete(req)
	case "import":
		return handleImport(req)
	case "data_read":
		return handleDataRead(req)
	case "invoke":
		return handleInvoke(req)
	default:
		return Response{Error: fmt.Sprintf("unknown operation: %q", req.Operation)}
	}
}

// ---------------------------------------------------------------------------
// Operation handlers
// ---------------------------------------------------------------------------

func handleDescribe() Response {
	resList := make([]string, 0, len(resources))
	for name := range resources {
		resList = append(resList, name)
	}
	dsList := make([]string, 0, len(dataSources))
	for name := range dataSources {
		dsList = append(dsList, name)
	}
	actionList := make([]string, 0, len(actions))
	for name := range actions {
		actionList = append(actionList, name)
	}
	return Response{
		Resources:   resList,
		DataSources: dsList,
		Actions:     actionList,
	}
}

func handleGetSchema(req Request) Response {
	if r, ok := resources[req.Resource]; ok {
		s := r.Schema()
		return Response{Schema: &s}
	}
	if d, ok := dataSources[req.Resource]; ok {
		s := d.DataSchema()
		return Response{Schema: &s}
	}
	if a, ok := actions[req.Resource]; ok {
		s := a.InputSchema()
		return Response{Schema: &s}
	}
	return Response{Error: fmt.Sprintf("unknown resource or data source: %q", req.Resource)}
}

func handleValidate(req Request) Response {
	r, ok := resources[req.Resource]
	if !ok {
		return Response{Error: fmt.Sprintf("unknown resource: %q", req.Resource)}
	}
	if err := r.Validate(req.Config); err != nil {
		return Response{Error: err.Error()}
	}
	return Response{}
}

func handleRead(req Request) Response {
	// Try resource first, then data source.
	if r, ok := resources[req.Resource]; ok {
		state, err := r.Read(req.State)
		if err != nil {
			return Response{Error: err.Error()}
		}
		return Response{State: state}
	}
	return Response{Error: fmt.Sprintf("unknown resource: %q", req.Resource)}
}

func handleCreate(req Request) Response {
	r, ok := resources[req.Resource]
	if !ok {
		return Response{Error: fmt.Sprintf("unknown resource: %q", req.Resource)}
	}
	state, err := r.Create(req.Plan)
	if err != nil {
		return Response{Error: err.Error()}
	}
	return Response{State: state}
}

func handleUpdate(req Request) Response {
	r, ok := resources[req.Resource]
	if !ok {
		return Response{Error: fmt.Sprintf("unknown resource: %q", req.Resource)}
	}
	state, err := r.Update(req.State, req.Plan)
	if err != nil {
		return Response{Error: err.Error()}
	}
	return Response{State: state}
}

func handleDelete(req Request) Response {
	r, ok := resources[req.Resource]
	if !ok {
		return Response{Error: fmt.Sprintf("unknown resource: %q", req.Resource)}
	}
	if err := r.Delete(req.State); err != nil {
		return Response{Error: err.Error()}
	}
	return Response{}
}

func handleImport(req Request) Response {
	r, ok := resources[req.Resource]
	if !ok {
		return Response{Error: fmt.Sprintf("unknown resource: %q", req.Resource)}
	}
	state, err := r.ImportState(req.ImportID)
	if err != nil {
		return Response{Error: err.Error()}
	}
	return Response{State: state}
}

func handleDataRead(req Request) Response {
	d, ok := dataSources[req.Resource]
	if !ok {
		return Response{Error: fmt.Sprintf("unknown data source: %q", req.Resource)}
	}
	state, err := d.DataRead(req.Config)
	if err != nil {
		return Response{Error: err.Error()}
	}
	return Response{State: state}
}

func handleInvoke(req Request) Response {
	a, ok := actions[req.Resource]
	if !ok {
		return Response{Error: fmt.Sprintf("unknown action: %q", req.Resource)}
	}

	state, err := a.Invoke(req.Config)
	if err != nil {
		return Response{Error: err.Error()}
	}

	return Response{State: state}
}

// ---------------------------------------------------------------------------
// Response helpers
// ---------------------------------------------------------------------------

func writeResponse(resp Response) {
	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(resp); err != nil {
		// Last resort -- if we can't even write JSON, dump a plain text error.
		fmt.Fprintf(os.Stderr, "pluginsdk: failed to encode response: %v\n", err)
		os.Exit(1)
	}
}

func writeError(msg string) {
	writeResponse(Response{Error: msg})
}
