package actions

import (
	"context"
	"fmt"
	"strings"
	"time"

	frameworkaction "github.com/hashicorp/terraform-plugin-framework/action"
	actionschema "github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/hostsession"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/transport"
)

var (
	_ frameworkaction.Action              = (*RestartHostAction)(nil)
	_ frameworkaction.ActionWithConfigure = (*RestartHostAction)(nil)
)

type RestartHostAction struct {
	typeName    string
	executorMgr *engine.ProviderData
}

type restartHostActionModel struct {
	Name           types.String `tfsdk:"name"`
	Reason         types.String `tfsdk:"reason"`
	RebootCommand  types.String `tfsdk:"reboot_command"`
	TimeoutSeconds types.Int64  `tfsdk:"timeout_seconds"`
	SettleSeconds  types.Int64  `tfsdk:"settle_seconds"`
	Target         types.String `tfsdk:"target"`
	Port           types.Int64  `tfsdk:"port"`
	Transport      types.String `tfsdk:"transport"`
}

func NewRestartHostAction(typeName string) *RestartHostAction {
	return &RestartHostAction{typeName: typeName}
}

func (a *RestartHostAction) Metadata(_ context.Context, _ frameworkaction.MetadataRequest, resp *frameworkaction.MetadataResponse) {
	resp.TypeName = a.typeName
}

func (a *RestartHostAction) Schema(_ context.Context, _ frameworkaction.SchemaRequest, resp *frameworkaction.SchemaResponse) {
	resp.Schema = actionschema.Schema{
		Description: "Safely reboot a target host and wait for it to return on a new boot instance.",
		Attributes: map[string]actionschema.Attribute{
			"name":            actionschema.StringAttribute{Description: "Logical reboot operation name.", Required: true},
			"reason":          actionschema.StringAttribute{Description: "Human-readable reboot reason.", Optional: true},
			"reboot_command":  actionschema.StringAttribute{Description: "Optional explicit reboot command.", Optional: true},
			"timeout_seconds": actionschema.Int64Attribute{Description: "Maximum time to wait for reboot and reconnect.", Optional: true},
			"settle_seconds":  actionschema.Int64Attribute{Description: "Additional settle time after boot proof before returning.", Optional: true},
			"target":          actionschema.StringAttribute{Description: "Target host or address for this action. Overrides provider default_target.target.", Optional: true},
			"port":            actionschema.Int64Attribute{Description: "Target port for this action. Overrides provider default_target.port.", Optional: true},
			"transport":       actionschema.StringAttribute{Description: "Transport for this action. The current provider surface supports ssh.", Optional: true},
		},
	}
}

func (a *RestartHostAction) Configure(_ context.Context, req frameworkaction.ConfigureRequest, resp *frameworkaction.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	pd, ok := req.ProviderData.(*engine.ProviderData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Provider Data", fmt.Sprintf("Expected *engine.ProviderData, got %T", req.ProviderData))
		return
	}

	a.executorMgr = pd
}

func (a *RestartHostAction) Invoke(ctx context.Context, req frameworkaction.InvokeRequest, resp *frameworkaction.InvokeResponse) {
	if a.executorMgr == nil {
		resp.Diagnostics.AddError("Action not configured", "Provider configuration was not available to the action.")
		return
	}

	var config restartHostActionModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := strings.TrimSpace(config.Name.ValueString())
	if name == "" {
		resp.Diagnostics.AddError("Missing reboot name", "The name attribute must not be empty.")
		return
	}

	sessionConfig, err := a.hostConfig(config)
	if err != nil {
		resp.Diagnostics.AddError("Invalid host configuration", err.Error())
		return
	}

	params := hostsession.RestartHostParams{
		Name:          name,
		Reason:        strings.TrimSpace(config.Reason.ValueString()),
		RebootCommand: strings.TrimSpace(config.RebootCommand.ValueString()),
	}
	if !config.TimeoutSeconds.IsNull() {
		params.Timeout = time.Duration(config.TimeoutSeconds.ValueInt64()) * time.Second
	}
	if !config.SettleSeconds.IsNull() {
		params.Settle = time.Duration(config.SettleSeconds.ValueInt64()) * time.Second
	}

	if resp.SendProgress != nil {
		resp.SendProgress(frameworkaction.InvokeProgressEvent{Message: fmt.Sprintf("Rebooting host for %s", name)})
	}

	session, err := a.executorMgr.Pool.GetOrCreate(ctx, sessionConfig)
	if err != nil {
		resp.Diagnostics.AddError("Get session failed", err.Error())
		return
	}

	if err := a.executorMgr.ExecutorMgr.RestartHost(ctx, session, params); err != nil {
		resp.Diagnostics.AddError("Host reboot failed", err.Error())
		return
	}

	if resp.SendProgress != nil {
		resp.SendProgress(frameworkaction.InvokeProgressEvent{Message: fmt.Sprintf("Host reboot completed for %s", name)})
	}
}

func (a *RestartHostAction) hostConfig(config restartHostActionModel) (transport.TransportConfig, error) {
	sessionConfig := transport.TransportConfig{}
	if a.executorMgr.DefaultHost != nil {
		sessionConfig = *a.executorMgr.DefaultHost
	}
	if !config.Target.IsNull() && !config.Target.IsUnknown() {
		sessionConfig.Target = strings.TrimSpace(config.Target.ValueString())
	}
	if !config.Port.IsNull() && !config.Port.IsUnknown() {
		sessionConfig.Port = int(config.Port.ValueInt64())
	}
	if !config.Transport.IsNull() && !config.Transport.IsUnknown() {
		sessionConfig.Transport = strings.TrimSpace(config.Transport.ValueString())
	}

	sessionConfig.Target = strings.TrimSpace(sessionConfig.Target)
	sessionConfig.Transport = strings.TrimSpace(sessionConfig.Transport)
	if sessionConfig.Transport == "" {
		sessionConfig.Transport = transport.TransportSSH
	}

	if sessionConfig.Port < 0 {
		return transport.TransportConfig{}, fmt.Errorf("port resolves to %d. Set a positive port value", sessionConfig.Port)
	}
	if sessionConfig.NormalizedTransport() != transport.TransportSSH && sessionConfig.NormalizedTransport() != transport.TransportLocal {
		return transport.TransportConfig{}, fmt.Errorf("transport resolves to %q. The current provider surface supports ssh", sessionConfig.Transport)
	}
	if sessionConfig.IsLocal() {
		if sessionConfig.Target == "" {
			sessionConfig.Target = transport.TransportLocal
		}
		return sessionConfig, nil
	}
	if sessionConfig.Target == "" {
		return transport.TransportConfig{}, fmt.Errorf("either set target in the action config or configure default_target on the provider")
	}
	if sessionConfig.Port == 0 {
		sessionConfig.Port = transport.DefaultSSHPort
	}
	return sessionConfig, nil
}
