//go:build !js && tinygo

package pluginsdk

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"unsafe"
)

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

type hostCallResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type runtimeHostProfile struct {
	Hostname      string   `json:"hostname"`
	DistroID      string   `json:"distro_id"`
	DistroName    string   `json:"distro_name"`
	DistroVersion string   `json:"distro_version"`
	DistroFamily  string   `json:"distro_family"`
	Kernel        string   `json:"kernel"`
	KernelVersion string   `json:"kernel_version"`
	Arch          string   `json:"arch"`
	InitSystem    string   `json:"init_system"`
	PackageMgr    string   `json:"package_manager"`
	AvailableCmds []string `json:"available_commands"`
	SELinux       bool     `json:"selinux"`
	AppArmor      bool     `json:"apparmor"`
}

var allocations = map[uint32][]byte{}
var lockHandles = map[uint32]string{}
var nextLockHandle uint32 = 1

//go:wasmimport host file_read
func hostFileRead(ptr, size uint32) uint64

//go:wasmimport host file_write
func hostFileWrite(ptr, size uint32) uint64

//go:wasmimport host dir_ensure
func hostDirEnsure(ptr, size uint32) uint64

//go:wasmimport host dir_read
func hostDirRead(ptr, size uint32) uint64

//go:wasmimport host file_delete
func hostFileDelete(ptr, size uint32) uint64

//go:wasmimport host file_exists
func hostFileExists(ptr, size uint32) uint64

//go:wasmimport host file_stat
func hostFileStat(ptr, size uint32) uint64

//go:wasmimport host file_chown
func hostFileChown(ptr, size uint32) uint64

//go:wasmimport host file_chmod
func hostFileChmod(ptr, size uint32) uint64

//go:wasmimport host file_lock
func hostFileLock(ptr, size uint32) uint64

//go:wasmimport host file_unlock
func hostFileUnlock(ptr, size uint32) uint64

//go:wasmimport host file_rename
func hostFileRename(ptr, size uint32) uint64

//go:wasmimport host file_readlink
func hostFileReadlink(ptr, size uint32) uint64

//go:wasmimport host file_symlink
func hostFileSymlink(ptr, size uint32) uint64

//go:wasmimport host identity_lookup_user
func hostIdentityLookupUser(ptr, size uint32) uint64

//go:wasmimport host identity_lookup_group
func hostIdentityLookupGroup(ptr, size uint32) uint64

//go:wasmimport host cmd_exec
func hostCmdExec(ptr, size uint32) uint64

//go:wasmimport host cmd_exists
func hostCmdExists(ptr, size uint32) uint64

//go:wasmimport host host_profile
func hostProfile(ptr, size uint32) uint64

//go:wasmimport host fetch_url
func hostFetchURL(ptr, size uint32) uint64

//go:wasmimport host log_info
func hostLogInfo(ptr, size uint32) uint64

//go:wasmimport host log_warn
func hostLogWarn(ptr, size uint32) uint64

func init() {
	FileRead = wasmFileRead
	FileWrite = wasmFileWrite
	DirEnsure = wasmDirEnsure
	ReadDir = wasmReadDir
	FileDelete = wasmFileDelete
	FileExists = wasmFileExists
	FileStat_ = wasmFileStat
	FileChown = wasmFileChown
	FileChmod = wasmFileChmod
	FileLock = wasmFileLock
	FileUnlock = wasmFileUnlock
	FileRename = wasmFileRename
	FileReadlink = wasmFileReadlink
	FileSymlink = wasmFileSymlink
	LookupUser = wasmLookupUser
	LookupGroup = wasmLookupGroup
	FetchURL = wasmFetchURL
	CmdExec = wasmCmdExec
	CmdExists = wasmCmdExists
	GetHostProfile = wasmGetHostProfile
	LogInfo = wasmLogInfo
	LogWarn = wasmLogWarn
}

//go:wasmexport alloc
func alloc(size uint32) uint32 {
	return allocBytes(size)
}

//go:wasmexport tf_nix_free
func tfNixFree(ptr uint32) {
	releaseBytes(ptr)
}

func allocBytes(size uint32) uint32 {
	if size == 0 {
		return 0
	}
	buf := make([]byte, size)
	ptr := uint32(uintptr(unsafe.Pointer(&buf[0])))
	allocations[ptr] = buf
	return ptr
}

func releaseBytes(ptr uint32) {
	if ptr == 0 {
		return
	}
	delete(allocations, ptr)
}

//go:wasmexport schema
func schema(ptr, size uint32) uint64 {
	return exportResponse(handleSchema(decodeModuleRequest(ptr, size)))
}

//go:wasmexport validate
func validate(ptr, size uint32) uint64 {
	return exportResponse(handleModuleRequest("validate", decodeModuleRequest(ptr, size)))
}

//go:wasmexport read
func read(ptr, size uint32) uint64 {
	return exportResponse(handleModuleRequest("read", decodeModuleRequest(ptr, size)))
}

//go:wasmexport create
func create(ptr, size uint32) uint64 {
	return exportResponse(handleModuleRequest("create", decodeModuleRequest(ptr, size)))
}

//go:wasmexport update
func update(ptr, size uint32) uint64 {
	return exportResponse(handleModuleRequest("update", decodeModuleRequest(ptr, size)))
}

//go:wasmexport delete
func deleteResource(ptr, size uint32) uint64 {
	return exportResponse(handleModuleRequest("delete", decodeModuleRequest(ptr, size)))
}

//go:wasmexport import
func importResource(ptr, size uint32) uint64 {
	return exportResponse(handleModuleRequest("import", decodeModuleRequest(ptr, size)))
}

//go:wasmexport data_read
func dataRead(ptr, size uint32) uint64 {
	return exportResponse(handleModuleRequest("data_read", decodeModuleRequest(ptr, size)))
}

//go:wasmexport invoke
func invoke(ptr, size uint32) uint64 {
	return exportResponse(handleModuleRequest("invoke", decodeModuleRequest(ptr, size)))
}

func decodeModuleRequest(ptr, size uint32) ModuleRequest {
	if ptr == 0 || size == 0 {
		return ModuleRequest{}
	}
	raw := append([]byte(nil), readBytes(ptr, size)...)
	var request ModuleRequest
	if err := json.Unmarshal(raw, &request); err == nil && request.ResourceType != "" {
		return request
	}
	var importID string
	if err := json.Unmarshal(raw, &importID); err == nil {
		return ModuleRequest{Plan: raw, State: raw, Config: raw, ImportID: importID}
	}
	return ModuleRequest{Plan: raw, State: raw, Config: raw}
}

func handleSchema(request ModuleRequest) interface{} {
	if request.ResourceType == "" {
		return Response{Resources: registeredResourceNames(), DataSources: registeredDataSourceNames(), Actions: registeredActionNames()}
	}
	return handleGetSchema(Request{Resource: request.ResourceType})
}

func handleModuleRequest(action string, request ModuleRequest) interface{} {
	req := Request{Operation: action, Resource: request.ResourceType, ProviderName: request.ProviderName}
	if req.Resource == "" {
		return Response{Error: "missing resource_type"}
	}

	switch action {
	case "validate":
		_ = json.Unmarshal(request.Config, &req.Config)
		if len(request.Config) == 0 && len(request.Plan) > 0 {
			_ = json.Unmarshal(request.Plan, &req.Config)
		}
	case "read":
		if len(request.State) > 0 {
			_ = json.Unmarshal(request.State, &req.State)
		} else if len(request.Plan) > 0 {
			_ = json.Unmarshal(request.Plan, &req.Plan)
		}
	case "create":
		_ = json.Unmarshal(request.Plan, &req.Plan)
	case "update":
		_ = json.Unmarshal(request.State, &req.State)
		_ = json.Unmarshal(request.Plan, &req.Plan)
	case "delete":
		_ = json.Unmarshal(request.State, &req.State)
	case "import":
		req.ImportID = request.ImportID
		if req.ImportID == "" && len(request.Config) > 0 {
			_ = json.Unmarshal(request.Config, &req.ImportID)
		}
	case "data_read":
		_ = json.Unmarshal(request.Config, &req.Config)
	case "invoke":
		_ = json.Unmarshal(request.Config, &req.Config)
	}

	return dispatch(req)
}

func exportResponse(payload interface{}) uint64 {
	var (
		data []byte
		err  error
	)

	switch typed := payload.(type) {
	case Response:
		data, err = typed.MarshalJSON()
	case *Response:
		if typed == nil {
			data = []byte("null")
		} else {
			data, err = typed.MarshalJSON()
		}
	default:
		data, err = json.Marshal(payload)
	}
	if err != nil {
		data = []byte(`{"error":"marshal failed"}`)
	}
	ptr := allocBytes(uint32(len(data)))
	copy(readBytes(ptr, uint32(len(data))), data)
	return uint64(ptr)<<32 | uint64(len(data))
}

func readBytes(ptr, size uint32) []byte {
	if ptr == 0 || size == 0 {
		return nil
	}
	buf, ok := allocations[ptr]
	if !ok || uint32(len(buf)) < size {
		return nil
	}
	return buf[:size]
}

func registeredResourceNames() []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(resources))
	for name, resource := range resources {
		if resource.Name() != name {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}

func registeredDataSourceNames() []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(dataSources))
	for name, ds := range dataSources {
		if ds.Name() != name {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}

func registeredActionNames() []string {
	result := make([]string, 0, len(actions))
	for name := range actions {
		result = append(result, name)
	}
	return result
}

func wasmFileRead(path string) ([]byte, error) {
	var content string
	if err := callHost("file_read", hostCallRequest{Path: path}, &content); err != nil {
		return nil, err
	}
	return []byte(content), nil
}

func wasmFileWrite(path string, data []byte, mode uint32) error {
	return callHost("file_write", hostCallRequest{
		Path:          path,
		ContentBase64: base64.StdEncoding.EncodeToString(data),
		Mode:          mode,
	}, nil)
}

func wasmDirEnsure(path string, mode uint32) error {
	return callHost("dir_ensure", hostCallRequest{Path: path, Mode: mode}, nil)
}

func wasmReadDir(path string) ([]DirEntry, error) {
	var entries []DirEntry
	err := callHost("dir_read", hostCallRequest{Path: path}, &entries)
	return entries, err
}

func wasmFileDelete(path string) error {
	return callHost("file_delete", hostCallRequest{Path: path}, nil)
}

func wasmFileExists(path string) (bool, error) {
	var exists bool
	err := callHost("file_exists", hostCallRequest{Path: path}, &exists)
	return exists, err
}

func wasmFileStat(path string) (*FileStat, error) {
	var stat FileStat
	err := callHost("file_stat", hostCallRequest{Path: path}, &stat)
	if err != nil {
		return nil, err
	}
	return &stat, nil
}

func wasmFileChown(path string, owner, group string) error {
	return callHost("file_chown", hostCallRequest{Path: path, Owner: owner, Group: group}, nil)
}

func wasmFileChmod(path string, mode uint32) error {
	return callHost("file_chmod", hostCallRequest{Path: path, Mode: mode}, nil)
}

func wasmFileLock(path string) (uint32, error) {
	if err := callHost("file_lock", hostCallRequest{Path: path}, nil); err != nil {
		return 0, err
	}
	handle := nextLockHandle
	nextLockHandle++
	lockHandles[handle] = path
	return handle, nil
}

func wasmFileUnlock(handle uint32) error {
	path, ok := lockHandles[handle]
	if !ok {
		return fmt.Errorf("unknown lock handle %d", handle)
	}
	delete(lockHandles, handle)
	return callHost("file_unlock", hostCallRequest{Path: path}, nil)
}

func wasmFileRename(from, to string) error {
	return callHost("file_rename", hostCallRequest{Path: from, Target: to}, nil)
}

func wasmFileReadlink(path string) (string, error) {
	var target string
	err := callHost("file_readlink", hostCallRequest{Path: path}, &target)
	return target, err
}

func wasmFileSymlink(target, path string) error {
	return callHost("file_symlink", hostCallRequest{Path: path, Target: target}, nil)
}

func wasmLookupUser(name string) (*IdentityUser, error) {
	var identity *IdentityUser
	err := callHost("identity_lookup_user", hostCallRequest{Name: name}, &identity)
	return identity, err
}

func wasmLookupGroup(name string) (*IdentityGroup, error) {
	var identity *IdentityGroup
	err := callHost("identity_lookup_group", hostCallRequest{Name: name}, &identity)
	return identity, err
}

func wasmCmdExec(cmd string, args []string) (*CmdResult, error) {
	var result CmdResult
	err := callHost("cmd_exec", hostCallRequest{Name: cmd, Args: args}, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func wasmCmdExists(name string) (bool, error) {
	var exists bool
	err := callHost("cmd_exists", hostCallRequest{Name: name}, &exists)
	return exists, err
}

func wasmGetHostProfile() (*HostProfile, error) {
	var raw runtimeHostProfile
	err := callHost("host_profile", hostCallRequest{}, &raw)
	if err != nil {
		return nil, err
	}
	return &HostProfile{
		Hostname:          raw.Hostname,
		Distro:            raw.DistroID,
		DistroVersion:     raw.DistroVersion,
		DistroFamily:      raw.DistroFamily,
		Kernel:            raw.Kernel,
		KernelVersion:     raw.KernelVersion,
		Arch:              raw.Arch,
		InitSystem:        raw.InitSystem,
		PackageManager:    raw.PackageMgr,
		AvailableCommands: append([]string(nil), raw.AvailableCmds...),
		SELinux:           raw.SELinux,
		AppArmor:          raw.AppArmor,
	}, nil
}

func wasmFetchURL(url string) ([]byte, error) {
	var contentBase64 string
	err := callHost("fetch_url", hostCallRequest{URL: url}, &contentBase64)
	if err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(contentBase64)
	if err != nil {
		return nil, fmt.Errorf("decode fetch_url response: %w", err)
	}
	return data, nil
}

func wasmLogInfo(msg string) {
	_ = callHost("log_info", hostCallRequest{Message: msg}, nil)
}

func wasmLogWarn(msg string) {
	_ = callHost("log_warn", hostCallRequest{Message: msg}, nil)
}

func callHost(funcName string, request interface{}, result interface{}) error {
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	ptr := allocBytes(uint32(len(data)))
	defer releaseBytes(ptr)
	copy(readBytes(ptr, uint32(len(data))), data)
	var packed uint64
	switch funcName {
	case "file_read":
		packed = hostFileRead(ptr, uint32(len(data)))
	case "file_write":
		packed = hostFileWrite(ptr, uint32(len(data)))
	case "dir_ensure":
		packed = hostDirEnsure(ptr, uint32(len(data)))
	case "dir_read":
		packed = hostDirRead(ptr, uint32(len(data)))
	case "file_delete":
		packed = hostFileDelete(ptr, uint32(len(data)))
	case "file_exists":
		packed = hostFileExists(ptr, uint32(len(data)))
	case "file_stat":
		packed = hostFileStat(ptr, uint32(len(data)))
	case "file_chown":
		packed = hostFileChown(ptr, uint32(len(data)))
	case "file_chmod":
		packed = hostFileChmod(ptr, uint32(len(data)))
	case "file_lock":
		packed = hostFileLock(ptr, uint32(len(data)))
	case "file_unlock":
		packed = hostFileUnlock(ptr, uint32(len(data)))
	case "file_rename":
		packed = hostFileRename(ptr, uint32(len(data)))
	case "file_readlink":
		packed = hostFileReadlink(ptr, uint32(len(data)))
	case "file_symlink":
		packed = hostFileSymlink(ptr, uint32(len(data)))
	case "identity_lookup_user":
		packed = hostIdentityLookupUser(ptr, uint32(len(data)))
	case "identity_lookup_group":
		packed = hostIdentityLookupGroup(ptr, uint32(len(data)))
	case "cmd_exec":
		packed = hostCmdExec(ptr, uint32(len(data)))
	case "cmd_exists":
		packed = hostCmdExists(ptr, uint32(len(data)))
	case "host_profile":
		packed = hostProfile(ptr, uint32(len(data)))
	case "fetch_url":
		packed = hostFetchURL(ptr, uint32(len(data)))
	case "log_info":
		packed = hostLogInfo(ptr, uint32(len(data)))
	case "log_warn":
		packed = hostLogWarn(ptr, uint32(len(data)))
	default:
		return fmt.Errorf("unknown host function: %s", funcName)
	}
	respPtr := uint32(packed >> 32)
	respLen := uint32(packed & 0xffffffff)
	if respLen == 0 {
		return fmt.Errorf("empty host response")
	}
	respBytes := append([]byte(nil), readBytes(respPtr, respLen)...)
	var response hostCallResponse
	if err := json.Unmarshal(respBytes, &response); err != nil {
		return err
	}
	if response.Error != "" {
		return fmt.Errorf("%s", response.Error)
	}
	if result != nil && response.Result != nil {
		if err := json.Unmarshal(response.Result, result); err != nil {
			return err
		}
	}
	return nil
}
