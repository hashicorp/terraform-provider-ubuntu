// Copyright IBM Corp. 2026

package engine

import (
	"context"
	"fmt"
	"strings"

	frameworkaction "github.com/hashicorp/terraform-plugin-framework/action"
	actionschema "github.com/hashicorp/terraform-plugin-framework/action/schema"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/hostsession"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/transport"
	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

// ResourcePattern identifies the behavioral pattern of a resource.
// The pattern-specific logic lives in the WASM plugin; the TF adapter
// is the same for all patterns.
type ResourcePattern string

const (
	PatternEntry    ResourcePattern = "entry"
	PatternConfig   ResourcePattern = "config"
	PatternCommand  ResourcePattern = "command"
	PatternFile     ResourcePattern = "file"
	PatternArtifact ResourcePattern = "artifact"
)

type ResourceSchemaCustomizer func(map[string]resourceschema.Attribute) map[string]resourceschema.Attribute

type DataSourceSchemaCustomizer func(map[string]datasourceschema.Attribute) map[string]datasourceschema.Attribute

type ActionSchemaCustomizer func(map[string]actionschema.Attribute) map[string]actionschema.Attribute

type ResourceExecutionPolicy func(action string, op *hostsession.OperationMessage) (*hostrpc.ExecutionContext, error)

type DataSourceExecutionPolicy func(action string, op *hostsession.OperationMessage) (*hostrpc.ExecutionContext, error)

type ActionExecutionPolicy func(config map[string]interface{}) (*hostrpc.ExecutionContext, error)

type ResourceResultShaper func(ctx context.Context, action string, state map[string]interface{}) (map[string]interface{}, diag.Diagnostics)

type ResourceImportShaper func(ctx context.Context, state map[string]interface{}) (map[string]interface{}, diag.Diagnostics)

type DataSourceResultShaper func(ctx context.Context, state map[string]interface{}) (map[string]interface{}, diag.Diagnostics)

type ActionResultShaper func(ctx context.Context, result map[string]interface{}) diag.Diagnostics

type ActionLockPlanner func(config map[string]interface{}) ([]hostsession.LockDescriptor, error)

type ActionInvoker func(ctx context.Context, req ActionInvokeRequest) (map[string]interface{}, error)

type ValidationPolicy struct {
	RemotePlanValidation bool
	PreWriteValidation   bool
	ReadAfterWrite       bool
}

type MissingImportIdentityError struct {
	Missing []string
}

func (e *MissingImportIdentityError) Error() string {
	if e == nil || len(e.Missing) == 0 {
		return "missing import identity"
	}
	return fmt.Sprintf("missing import identity fields: %s", strings.Join(e.Missing, ", "))
}

type ImportIDFormatter func(values map[string]interface{}) (string, error)

type ResourceImportIdentity struct {
	Attributes map[string]identityschema.Attribute
	FormatID   ImportIDFormatter
}

type DestroySafetyMode string

const (
	DestroySafetyModeNone           DestroySafetyMode = "none"
	DestroySafetyModeNoDestroy      DestroySafetyMode = "no_destroy"
	DestroySafetyModeCriticalObject DestroySafetyMode = "critical_object"
	DestroySafetyModeExplicitAllow  DestroySafetyMode = "explicit_allow"
)

type DestroySafetyGuard func(state map[string]interface{}, cfg DestroySafetyConfig) (bool, string)

type ExplicitAllowPredicate func(state map[string]interface{}) bool

type DestroySafetyPolicy struct {
	Mode                  DestroySafetyMode
	Guard                 DestroySafetyGuard
	RequiresExplicitAllow ExplicitAllowPredicate
}

type ActionInvokeRequest struct {
	Manager       *hostsession.ExecutorManager
	Session       *transport.Session
	HostConfig    transport.TransportConfig
	RuntimeType   string
	RuntimeModule string
	Config        map[string]interface{}
	Execution     *hostrpc.ExecutionContext
	Locks         []hostsession.LockDescriptor
	Progress      func(frameworkaction.InvokeProgressEvent)
}

// ResourceDefinition describes a resource that can be registered with the provider.
type ResourceDefinition struct {
	// TypeName is the short resource name without the provider prefix,
	// e.g. "hosts_entry".
	TypeName string

	// Pattern identifies the behavioral category (entry, config, command, etc.).
	Pattern ResourcePattern

	// RequiredPrivilege is the minimum privilege level needed on the target
	// host: "root", "user", or "dynamic".
	RequiredPrivilege string

	// RuntimeType is the logical resource kind handled by the executor, e.g.
	// "hosts_entry".
	RuntimeType string

	// RuntimeModule is the preferred WASM module or capability pack asset that
	// should be loaded to handle this resource.
	RuntimeModule string

	// Attributes contains the resource-specific terraform-plugin-framework
	// schema attributes. The adapter automatically adds the common "id"
	// (computed) and "host" (optional) attributes.
	Attributes map[string]resourceschema.Attribute

	// Blocks contains resource-specific terraform-plugin-framework blocks.
	Blocks map[string]resourceschema.Block

	// CustomizeSchema can adjust the resource-specific schema before the
	// generic adapter adds common attributes.
	CustomizeSchema ResourceSchemaCustomizer

	// LockPlanner returns the scheduler lock set required for a specific action.
	// When nil, the generic adapter falls back to coarse host-level locking.
	LockPlanner LockPlanner

	// ExecutionPolicy can override the default privilege-based execution context.
	ExecutionPolicy ResourceExecutionPolicy

	// ShapeResult can normalize plugin state before it is written to Terraform.
	ShapeResult ResourceResultShaper

	// ShapeImport can normalize imported state before it is written to Terraform.
	ShapeImport ResourceImportShaper

	// ValidationPolicy controls when the generic adapter should invoke the
	// plugin Validate hook and whether it should read back state after writes.
	ValidationPolicy ValidationPolicy

	// DestroySafety controls whether workspace destroy or replacement deletes
	// should be blocked for critical host objects or explicit destructive
	// operations unless the user opts in.
	DestroySafety DestroySafetyPolicy

	// ImportRequiredOnExisting blocks create when the target object already
	// exists on the host and is not already in Terraform state.
	ImportRequiredOnExisting bool

	// ImportIdentity defines the structured import identity and canonical import
	// ID formatter used by the generic adapter for import blocks and existence
	// probes.
	ImportIdentity *ResourceImportIdentity
}

// DataSourceDefinition describes a data source that can be registered with the provider.
type DataSourceDefinition struct {
	// TypeName is the short data source name without the provider prefix.
	TypeName string

	// RuntimeType is the logical data source kind handled by the hostsession.
	RuntimeType string

	// RuntimeModule is the preferred WASM module or capability pack asset that
	// should be loaded to handle this data source.
	RuntimeModule string

	// Attributes contains the data-source-specific terraform-plugin-framework
	// schema attributes. The adapter automatically adds common attributes.
	Attributes map[string]datasourceschema.Attribute

	// CustomizeSchema can adjust the data-source-specific schema before the
	// generic adapter adds common attributes.
	CustomizeSchema DataSourceSchemaCustomizer

	// LockPlanner returns the scheduler lock set required for the read.
	// When nil, the generic adapter falls back to a shared host lock.
	LockPlanner func(action string, op *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error)

	// ExecutionPolicy can override the default privilege-based execution context.
	ExecutionPolicy DataSourceExecutionPolicy

	// ShapeResult can normalize plugin state before it is written to Terraform.
	ShapeResult DataSourceResultShaper
}

// ActionDefinition describes a generic imperative action that can be registered
// with the provider.
type ActionDefinition struct {
	// TypeName is the short action name without the provider prefix.
	TypeName string

	// RequiredPrivilege is the minimum privilege level needed on the target
	// host to execute the action.
	RequiredPrivilege string

	// RuntimeType is the logical action kind handled by the hostsession.
	RuntimeType string

	// RuntimeModule is the preferred WASM module or capability pack asset that
	// should be loaded to handle this action.
	RuntimeModule string

	// Attributes contains the action-specific terraform-plugin-framework
	// schema attributes. The adapter automatically adds the common "host"
	// attribute.
	Attributes map[string]actionschema.Attribute

	// CustomizeSchema can adjust the action-specific schema before the generic
	// adapter adds common attributes.
	CustomizeSchema ActionSchemaCustomizer

	// LockPlanner returns the scheduler lock set required to invoke the action.
	LockPlanner ActionLockPlanner

	// ExecutionPolicy can override the default privilege-based execution context.
	ExecutionPolicy ActionExecutionPolicy

	// Invoke overrides the default runtime invoke path for actions that need
	// custom execution behavior after config resolution.
	Invoke ActionInvoker

	// ShapeResult can convert an action result into diagnostics or warnings.
	ShapeResult ActionResultShaper
}
