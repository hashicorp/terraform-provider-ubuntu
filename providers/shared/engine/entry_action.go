package engine

import (
	"context"
	"fmt"

	frameworkaction "github.com/hashicorp/terraform-plugin-framework/action"
	actionschema "github.com/hashicorp/terraform-plugin-framework/action/schema"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/hostsession"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/transport"
	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

var (
	_                frameworkaction.Action              = (*GenericAction)(nil)
	_                frameworkaction.ActionWithConfigure = (*GenericAction)(nil)
	getActionSession                                     = func(ctx context.Context, pool *transport.ConnectionPool, hostConfig transport.TransportConfig) (*transport.Session, error) {
		return pool.GetOrCreate(ctx, hostConfig)
	}
	ensureActionHostReady = func(ctx context.Context, manager *hostsession.ExecutorManager, session *transport.Session, needRoot bool) error {
		return manager.EnsureHostReady(ctx, session, needRoot)
	}
	invokeActionLocked = func(ctx context.Context, manager *hostsession.ExecutorManager, session *transport.Session, params hostrpc.ActionInvokeParams, locks []hostsession.LockDescriptor) (map[string]interface{}, error) {
		return manager.InvokeActionLocked(ctx, session, params, locks)
	}
)

type GenericAction struct {
	typeName          string
	runtimeType       string
	runtimeModule     string
	requiredPrivilege string
	attributes        map[string]actionschema.Attribute
	schemaCustomizer  ActionSchemaCustomizer
	lockPlanner       ActionLockPlanner
	executionPolicy   ActionExecutionPolicy
	invoker           ActionInvoker
	shapeResult       ActionResultShaper

	executorMgr *ProviderData
}

func NewGenericAction(typeName string, def ActionDefinition) *GenericAction {
	return &GenericAction{
		typeName:          typeName,
		runtimeType:       def.RuntimeType,
		runtimeModule:     def.RuntimeModule,
		requiredPrivilege: def.RequiredPrivilege,
		attributes:        def.Attributes,
		schemaCustomizer:  def.CustomizeSchema,
		lockPlanner:       def.LockPlanner,
		executionPolicy:   def.ExecutionPolicy,
		invoker:           def.Invoke,
		shapeResult:       def.ShapeResult,
	}
}

func (a *GenericAction) Metadata(_ context.Context, _ frameworkaction.MetadataRequest, resp *frameworkaction.MetadataResponse) {
	resp.TypeName = a.typeName
}

func (a *GenericAction) Schema(_ context.Context, _ frameworkaction.SchemaRequest, resp *frameworkaction.SchemaResponse) {
	resp.Schema = actionschema.Schema{
		Description: fmt.Sprintf("Invokes the %s action via the hostsession.", a.runtimeType),
		Attributes:  actionSchemaAttributes(a.attributes, a.schemaCustomizer),
	}
}

func (a *GenericAction) Configure(_ context.Context, req frameworkaction.ConfigureRequest, resp *frameworkaction.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	pd, ok := req.ProviderData.(*ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Provider Data",
			fmt.Sprintf("Expected *engine.ProviderData, got %T", req.ProviderData),
		)
		return
	}

	a.executorMgr = pd
}

func (a *GenericAction) Invoke(ctx context.Context, req frameworkaction.InvokeRequest, resp *frameworkaction.InvokeResponse) {
	if a.executorMgr == nil {
		resp.Diagnostics.AddError("Action not configured", "Provider configuration was not available to the action.")
		return
	}

	hostConfig, diagnostics := resolveHostFromConfig(ctx, &req.Config, a.executorMgr.DefaultHost)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	configAttrs, diagnostics := extractActionValuesFromConfig(ctx, &req.Config, a.attributes)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	execution, err := resolveActionExecutionContext(ActionDefinition{
		RequiredPrivilege: a.requiredPrivilege,
		ExecutionPolicy:   a.executionPolicy,
	}, configAttrs)
	if err != nil {
		resp.Diagnostics.AddError("Resolve execution context failed", err.Error())
		return
	}

	locks, err := planActionLocks("invoke", configAttrs, a.lockPlanner)
	if err != nil {
		resp.Diagnostics.AddError("Plan locks failed", err.Error())
		return
	}

	session, err := getActionSession(ctx, a.executorMgr.Pool, hostConfig)
	if err != nil {
		resp.Diagnostics.AddError("Get session failed", err.Error())
		return
	}

	if err := ensureActionHostReady(ctx, a.executorMgr.ExecutorMgr, session, execution != nil && execution.Become); err != nil {
		resp.Diagnostics.AddError("Host readiness failed", annotateActionError(hostConfig, a.runtimeType, configAttrs, fmt.Errorf("host readiness preflight failed: %w", err)).Error())
		return
	}

	var result map[string]interface{}
	if a.invoker != nil {
		result, err = a.invoker(ctx, ActionInvokeRequest{
			Manager:       a.executorMgr.ExecutorMgr,
			Session:       session,
			HostConfig:    hostConfig,
			RuntimeType:   a.runtimeType,
			RuntimeModule: a.runtimeModule,
			Config:        configAttrs,
			Execution:     execution,
			Locks:         locks,
			Progress:      resp.SendProgress,
		})
	} else {
		result, err = invokeActionLocked(ctx, a.executorMgr.ExecutorMgr, session, hostrpc.ActionInvokeParams{
			ModuleName:   a.runtimeModule,
			ResourceType: a.runtimeType,
			Config:       mustMarshalJSON(configAttrs),
			Execution:    execution,
		}, locks)
	}
	if err != nil {
		resp.Diagnostics.AddError("Action failed", err.Error())
		return
	}

	if a.shapeResult != nil {
		resp.Diagnostics.Append(a.shapeResult(ctx, result)...)
	}
}
