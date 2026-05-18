package pluginsdk

import (
	"encoding/json"
	"fmt"
	"reflect"
)

type HostCapabilityErrorKind string

const (
	HostCapabilityErrorUnsupportedHost HostCapabilityErrorKind = "unsupported_host"
	HostCapabilityErrorConfiguration   HostCapabilityErrorKind = "configuration"
	HostCapabilityErrorProgramming     HostCapabilityErrorKind = "programming"
)

// HostCapabilityError describes a missing host capability at the plugin runtime boundary.
type HostCapabilityError struct {
	Capability string
	Operation  string
	Kind       HostCapabilityErrorKind
	Detail     string
}

func (e *HostCapabilityError) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := fmt.Sprintf("pluginsdk host capability %q is unavailable during %s (%s)", e.Capability, e.Operation, e.Kind)
	if e.Detail != "" {
		return message + ": " + e.Detail
	}
	return message
}

// FileStat holds metadata returned by file_stat.
type FileStat struct {
	Path    string `json:"path,omitempty"`
	Mode    uint32 `json:"mode"`
	UID     uint32 `json:"uid,omitempty"`
	GID     uint32 `json:"gid,omitempty"`
	Owner   string `json:"owner"`
	Group   string `json:"group"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mod_time,omitempty"`
	IsDir   bool   `json:"is_dir,omitempty"`
	Digest  string `json:"digest"`
}

// DirEntry represents a single directory entry returned by dir_read.
type DirEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir,omitempty"`
}

// CmdResult holds the output of a command execution.
type CmdResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

// IdentityGroup holds group metadata returned by identity lookup capabilities.
type IdentityGroup struct {
	Name string `json:"name"`
	GID  int    `json:"gid"`
}

// IdentityUser holds user metadata returned by identity lookup capabilities.
type IdentityUser struct {
	Name         string   `json:"name"`
	UID          int      `json:"uid"`
	GID          int      `json:"gid"`
	Comment      string   `json:"comment,omitempty"`
	Home         string   `json:"home,omitempty"`
	Shell        string   `json:"shell,omitempty"`
	PrimaryGroup string   `json:"primary_group,omitempty"`
	Groups       []string `json:"groups,omitempty"`
}

// HostEntry represents a single line in /etc/hosts.
type HostEntry struct {
	IP       string   `json:"ip"`
	Hostname string   `json:"hostname"`
	Aliases  []string `json:"aliases,omitempty"`
	Comment  string   `json:"comment,omitempty"`
	Raw      string   `json:"raw,omitempty"`
	IsBlank  bool     `json:"is_blank,omitempty"`
}

// CrontabLine represents a single line in a per-user crontab.
type CrontabLine struct {
	Raw        string `json:"raw,omitempty"`
	IsBlank    bool   `json:"is_blank,omitempty"`
	IsComment  bool   `json:"is_comment,omitempty"`
	IsEnv      bool   `json:"is_env,omitempty"`
	Comment    string `json:"comment,omitempty"`
	Special    string `json:"special,omitempty"`
	Minute     string `json:"minute,omitempty"`
	Hour       string `json:"hour,omitempty"`
	DayOfMonth string `json:"day_of_month,omitempty"`
	Month      string `json:"month,omitempty"`
	DayOfWeek  string `json:"day_of_week,omitempty"`
	Command    string `json:"command,omitempty"`
}

// HostProfile describes the target system.
type HostProfile struct {
	Hostname          string   `json:"hostname"`
	Distro            string   `json:"distro"`
	DistroVersion     string   `json:"distro_version"`
	DistroFamily      string   `json:"distro_family"`
	Kernel            string   `json:"kernel"`
	KernelVersion     string   `json:"kernel_version"`
	Arch              string   `json:"arch"`
	InitSystem        string   `json:"init_system"`
	PackageManager    string   `json:"package_manager"`
	AvailableCommands []string `json:"available_commands,omitempty"`
	SELinux           bool     `json:"selinux"`
	AppArmor          bool     `json:"apparmor"`
	ProcFS            bool     `json:"procfs"`
	SysFS             bool     `json:"sysfs"`
}

// Schema describes the attributes a resource, data source, or action input exposes.
// Keys are attribute or block names; values describe the surface.
type Schema struct {
	Attributes map[string]Attribute `json:"attributes"`
	Blocks     map[string]Block     `json:"blocks,omitempty"`
}

// Attribute describes a single schema attribute.
type Attribute struct {
	Type        AttrType `json:"type"`
	Required    bool     `json:"required,omitempty"`
	Optional    bool     `json:"optional,omitempty"`
	Computed    bool     `json:"computed,omitempty"`
	Sensitive   bool     `json:"sensitive,omitempty"`
	Description string   `json:"description,omitempty"`
}

// Block describes a nested block in a resource schema.
type Block struct {
	Kind        BlockKind            `json:"kind"`
	Description string               `json:"description,omitempty"`
	Attributes  map[string]Attribute `json:"attributes,omitempty"`
}

// BlockKind enumerates the supported nested block shapes.
type BlockKind string

// AttrType enumerates the supported attribute types.
type AttrType string

const (
	AttrString AttrType = "string"
	AttrInt    AttrType = "int"
	AttrBool   AttrType = "bool"
	AttrList   AttrType = "list"
	AttrMap    AttrType = "map"

	BlockSingleNested BlockKind = "single_nested"
)

// StateData is the bag of key/value pairs that represents resource or data
// source state flowing between the provider framework and the plugin.
type StateData map[string]interface{}

// GetString returns a string attribute or "" if missing/wrong type.
func (s StateData) GetString(key string) string {
	v, ok := s[key]
	if !ok {
		return ""
	}
	str, ok := v.(string)
	if !ok {
		return ""
	}
	return str
}

// GetInt returns an int attribute or 0 if missing/wrong type.
func (s StateData) GetInt(key string) int {
	v, ok := s[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n) // JSON numbers decode as float64
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}

// GetBool returns a bool attribute or false if missing/wrong type.
func (s StateData) GetBool(key string) bool {
	v, ok := s[key]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	if !ok {
		return false
	}
	return b
}

// GetStringList returns a []string attribute or nil if missing/wrong type.
func (s StateData) GetStringList(key string) []string {
	v, ok := s[key]
	if !ok {
		return nil
	}
	switch raw := v.(type) {
	case []string:
		out := make([]string, len(raw))
		copy(out, raw)
		return out
	case []interface{}:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if str, ok := item.(string); ok {
				out = append(out, str)
			}
		}
		return out
	default:
		return nil
	}
}

// GetMap returns a map[string]string attribute or nil if missing/wrong type.
func (s StateData) GetMap(key string) map[string]string {
	v, ok := s[key]
	if !ok {
		return nil
	}
	switch raw := v.(type) {
	case map[string]string:
		out := make(map[string]string, len(raw))
		for k, val := range raw {
			out[k] = val
		}
		return out
	case map[string]interface{}:
		out := make(map[string]string, len(raw))
		for k, val := range raw {
			if str, ok := val.(string); ok {
				out[k] = str
			}
		}
		return out
	default:
		return nil
	}
}

// Resource is the interface plugins implement for managed resources.
type Resource interface {
	// Name returns the short runtime resource name (e.g. "file").
	Name() string
	// Schema returns the resource schema.
	Schema() Schema
	// Read refreshes state. Return the current state, or nil if the resource
	// no longer exists.
	Read(state StateData) (StateData, error)
	// Create provisions the resource. Return the new state.
	Create(plan StateData) (StateData, error)
	// Update modifies the resource in place. prior is the old state, plan is
	// the desired new state. Return the resulting state.
	Update(prior, plan StateData) (StateData, error)
	// Delete destroys the resource.
	Delete(state StateData) error
	// Validate checks the config before any CRUD operation.
	Validate(config StateData) error
	// ImportState produces state from an external identifier.
	ImportState(id string) (StateData, error)
}

// DataSource is the interface plugins implement for read-only data sources.
type DataSource interface {
	// Name returns the short runtime data source name (e.g. "file_info").
	Name() string
	// DataSchema returns the data source schema.
	DataSchema() Schema
	// DataRead fetches data. Return the resulting state.
	DataRead(config StateData) (StateData, error)
}

// Action is the interface plugins implement for imperative actions.
type Action interface {
	// Name returns the short runtime action name (e.g. "restart_process").
	Name() string
	// InputSchema returns the action input schema.
	InputSchema() Schema
	// Invoke resolves and/or executes the action, returning arbitrary state.
	Invoke(config StateData) (StateData, error)
}

// Request is the JSON envelope the executor sends to a plugin on stdin.
type Request struct {
	Operation string    `json:"operation"`
	Resource  string    `json:"resource"`
	State     StateData `json:"state,omitempty"`
	Plan      StateData `json:"plan,omitempty"`
	Config    StateData `json:"config,omitempty"`
	ImportID  string    `json:"import_id,omitempty"`
}

// Response is the JSON envelope the plugin writes to stdout.
type Response struct {
	State       StateData `json:"state,omitempty"`
	Error       string    `json:"error,omitempty"`
	Diagnostics []string  `json:"diagnostics,omitempty"`
	Schema      *Schema   `json:"schema,omitempty"`
	DataSources []string  `json:"data_sources,omitempty"`
	Resources   []string  `json:"resources,omitempty"`
	Actions     []string  `json:"actions,omitempty"`
}

func (r Response) MarshalJSON() ([]byte, error) {
	type responseAlias Response

	alias := responseAlias(r)
	if alias.State != nil {
		alias.State = normalizeStateDataForJSON(alias.State)
	}

	return json.Marshal(alias)
}

func normalizeStateDataForJSON(state StateData) StateData {
	if state == nil {
		return nil
	}

	out := make(StateData, len(state))
	for key, value := range state {
		out[key] = normalizeJSONValue(value)
	}
	return out
}

func normalizeJSONValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case nil:
		return nil
	case StateData:
		return normalizeStateDataForJSON(typed)
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			out[key] = normalizeJSONValue(item)
		}
		return out
	case map[string]string:
		out := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			out[key] = item
		}
		return out
	case []interface{}:
		if typed == nil {
			return nil
		}
		out := make([]interface{}, len(typed))
		for index, item := range typed {
			out[index] = normalizeJSONValue(item)
		}
		return out
	case []string:
		if typed == nil {
			return nil
		}
		out := make([]interface{}, len(typed))
		for index, item := range typed {
			out[index] = item
		}
		return out
	}

	rv := reflect.ValueOf(value)
	if !rv.IsValid() {
		return nil
	}

	switch rv.Kind() {
	case reflect.Map:
		if rv.IsNil() || rv.Type().Key().Kind() != reflect.String {
			return value
		}
		out := make(map[string]interface{}, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = normalizeJSONValue(iter.Value().Interface())
		}
		return out
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return nil
		}
		out := make([]interface{}, rv.Len())
		for index := 0; index < rv.Len(); index++ {
			out[index] = normalizeJSONValue(rv.Index(index).Interface())
		}
		return out
	default:
		return value
	}
}

type ModuleRequest struct {
	ResourceType string          `json:"resource_type,omitempty"`
	Action       string          `json:"action,omitempty"`
	State        json.RawMessage `json:"state,omitempty"`
	Plan         json.RawMessage `json:"plan,omitempty"`
	Config       json.RawMessage `json:"config,omitempty"`
	ImportID     string          `json:"import_id,omitempty"`
}
