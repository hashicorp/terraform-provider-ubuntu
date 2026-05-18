package engine

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/hostsession"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/transport"
)

var (
	_                       datasource.DataSource              = (*GenericDataSource)(nil)
	_                       datasource.DataSourceWithConfigure = (*GenericDataSource)(nil)
	sendDataSourceOperation                                    = sendOperation
)

type GenericDataSource struct {
	typeName         string
	runtimeType      string
	runtimeModule    string
	attributes       map[string]datasourceschema.Attribute
	schemaCustomizer DataSourceSchemaCustomizer
	lockPlanner      func(action string, op *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error)
	executionPolicy  DataSourceExecutionPolicy
	shapeResult      DataSourceResultShaper

	executorMgr *hostsession.ExecutorManager
	pool        *transport.ConnectionPool
	defaultHost *transport.TransportConfig
}

func NewGenericDataSource(typeName string, def DataSourceDefinition) *GenericDataSource {
	return &GenericDataSource{
		typeName:         typeName,
		runtimeType:      def.RuntimeType,
		runtimeModule:    def.RuntimeModule,
		attributes:       def.Attributes,
		schemaCustomizer: def.CustomizeSchema,
		lockPlanner:      def.LockPlanner,
		executionPolicy:  def.ExecutionPolicy,
		shapeResult:      def.ShapeResult,
	}
}

func (d *GenericDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = d.typeName
}

func (d *GenericDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = datasourceschema.Schema{
		Description: fmt.Sprintf("Reads %s data via the hostsession.", d.runtimeType),
		Attributes:  dataSourceSchemaAttributes(d.attributes, d.schemaCustomizer),
	}
}

func (d *GenericDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

	d.executorMgr = pd.ExecutorMgr
	d.pool = pd.Pool
	d.defaultHost = pd.DefaultHost
}

func (d *GenericDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	hostConfig, diagnostics := resolveHostFromConfig(ctx, &req.Config, d.defaultHost)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	configAttrs, diagnostics := configToJSON(ctx, &req.Config, d.attributes)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	op := &hostsession.OperationMessage{
		ModuleName:   d.runtimeModule,
		ResourceType: d.runtimeType,
		Action:       "data_read",
		Config:       configAttrs,
	}

	execution, err := resolveDataSourceExecutionContext(DataSourceDefinition{
		ExecutionPolicy: d.executionPolicy,
	}, "data_read", op)
	if err != nil {
		resp.Diagnostics.AddError("Resolve execution context failed", err.Error())
		return
	}
	op.Execution = execution

	locks, err := planDataSourceLocks("data_read", op, d.lockPlanner)
	if err != nil {
		resp.Diagnostics.AddError("Plan locks failed", err.Error())
		return
	}

	result, err := sendDataSourceOperation(ctx, d.executorMgr, d.pool, hostConfig, *op, locks)
	if err != nil {
		resp.Diagnostics.AddError("Read failed", err.Error())
		return
	}

	state := result.State
	if d.shapeResult != nil {
		state, diagnostics = d.shapeResult(ctx, state)
		resp.Diagnostics.Append(diagnostics...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	resp.Diagnostics.Append(setDataSourceStateFromJSON(ctx, state, d.attributes, &resp.State)...)
	resp.Diagnostics.Append(preserveDataSourceHostFromConfig(ctx, &req.Config, &resp.State)...)
}

func configToJSON(ctx context.Context, config *tfsdk.Config, attrs map[string]datasourceschema.Attribute) (map[string]interface{}, diag.Diagnostics) {
	return extractDataSourceValuesFromConfig(ctx, config, attrs)
}
