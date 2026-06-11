// Copyright IBM Corp. 2026

package serving

import (
	"context"
	"fmt"
	"strings"
	"time"

	frameworkaction "github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/assets"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/hostsession"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/supportpolicy"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/transport"
)

// Ensure BaseProvider satisfies the provider.Provider interface.
var (
	_ provider.Provider            = (*BaseProvider)(nil)
	_ provider.ProviderWithActions = (*BaseProvider)(nil)
)

// ProviderConfig is passed from the provider's main.go to configure the
// base provider with embedded binaries and plugin WASM modules.
type ProviderConfig struct {
	Name          string       // provider name e.g. "ubuntu"
	Prefix        string       // resource prefix e.g. "ubuntu"
	Assets        assets.Store // runtime asset source for executors and WASM modules
	SupportPolicy string       // support policy id for host admission
	Version       string       // provider version reported through provider metadata
}

func (c ProviderConfig) ResourceType(shortName string) string {
	return c.prefixedType(shortName)
}

func (c ProviderConfig) DataSourceType(shortName string) string {
	return c.prefixedType(shortName)
}

func (c ProviderConfig) ActionType(shortName string) string {
	return c.prefixedType(shortName)
}

func (c ProviderConfig) prefixedType(shortName string) string {
	if c.Prefix == "" {
		return shortName
	}
	return c.Prefix + "_" + shortName
}

// ResourceFactory is a function that returns a new resource.Resource instance.
type ResourceFactory func() resource.Resource

// DataSourceFactory is a function that returns a new datasource.DataSource instance.
type DataSourceFactory func() datasource.DataSource

// ActionFactory is a function that returns a new action.Action instance.
type ActionFactory func() frameworkaction.Action

// BaseProvider implements provider.Provider and provides the core
// infrastructure (connection pool, executor manager) that all resources
// and data sources in the provider share.
type BaseProvider struct {
	config      ProviderConfig
	pool        *transport.ConnectionPool
	manager     *hostsession.ExecutorManager
	resources   []ResourceFactory
	dataSources []DataSourceFactory
	actions     []ActionFactory
}

// NewBaseProvider creates a new BaseProvider with the given configuration.
func NewBaseProvider(config ProviderConfig) *BaseProvider {
	return &BaseProvider{
		config: config,
	}
}

// RegisterResource adds a resource factory to the provider.
func (p *BaseProvider) RegisterResource(factory ResourceFactory) {
	p.resources = append(p.resources, factory)
}

// RegisterDataSource adds a data source factory to the provider.
func (p *BaseProvider) RegisterDataSource(factory DataSourceFactory) {
	p.dataSources = append(p.dataSources, factory)
}

// RegisterAction adds an action factory to the provider.
func (p *BaseProvider) RegisterAction(factory ActionFactory) {
	p.actions = append(p.actions, factory)
}

// Pool returns the connection pool. Available after Configure() has run.
func (p *BaseProvider) Pool() *transport.ConnectionPool {
	return p.pool
}

// Manager returns the executor manager. Available after Configure() has run.
func (p *BaseProvider) Manager() *hostsession.ExecutorManager {
	return p.manager
}

// providerModel maps the HCL provider configuration block to Go types
// for terraform-plugin-framework.
type providerModel struct {
	SSH                               *sshModel           `tfsdk:"ssh"`
	DefaultTarget                     *defaultTargetModel `tfsdk:"default_target"`
	DestroySafety                     *destroySafetyModel `tfsdk:"destroy_safety"`
	EncryptedTunnel                   types.Bool          `tfsdk:"encrypted_tunnel"`
	UsePostQuantumHashes              types.Bool          `tfsdk:"use_post_quantum_hashes"`
	DualPluginVerification            types.Bool          `tfsdk:"dual_plugin_verification"`
	MaxConnections                    types.Int64         `tfsdk:"max_connections"`
	SSHDialTimeoutSeconds             types.Int64         `tfsdk:"ssh_dial_timeout_seconds"`
	SSHHandshakeTimeoutSeconds        types.Int64         `tfsdk:"ssh_handshake_timeout_seconds"`
	SSHConnectRetryAttempts           types.Int64         `tfsdk:"ssh_connect_retry_attempts"`
	SSHConnectRetryInitialBackoffMs   types.Int64         `tfsdk:"ssh_connect_retry_initial_backoff_ms"`
	SSHConnectRetryMaxBackoffMs       types.Int64         `tfsdk:"ssh_connect_retry_max_backoff_ms"`
	SSHConnectRetryTimeoutSeconds     types.Int64         `tfsdk:"ssh_connect_retry_timeout_seconds"`
	SSHReconnectRetryAttempts         types.Int64         `tfsdk:"ssh_reconnect_retry_attempts"`
	SSHReconnectRetryInitialBackoffMs types.Int64         `tfsdk:"ssh_reconnect_retry_initial_backoff_ms"`
	SSHReconnectRetryMaxBackoffMs     types.Int64         `tfsdk:"ssh_reconnect_retry_max_backoff_ms"`
	SSHReconnectRetryTimeoutSeconds   types.Int64         `tfsdk:"ssh_reconnect_retry_timeout_seconds"`
	HostLockTimeoutSeconds            types.Int64         `tfsdk:"host_lock_timeout_seconds"`
	RetryAttempts                     types.Int64         `tfsdk:"retry_attempts"`
	RetryInitialBackoffMs             types.Int64         `tfsdk:"retry_initial_backoff_ms"`
	RetryMaxBackoffMs                 types.Int64         `tfsdk:"retry_max_backoff_ms"`
}

type sshModel struct {
	User           types.String `tfsdk:"user"`
	PrivateKey     types.String `tfsdk:"private_key"`
	Certificate    types.String `tfsdk:"certificate"`
	Agent          types.Bool   `tfsdk:"agent"`
	KnownHostsFile types.String `tfsdk:"known_hosts_file"`
}

type defaultTargetModel struct {
	Target    types.String `tfsdk:"target"`
	Port      types.Int64  `tfsdk:"port"`
	Transport types.String `tfsdk:"transport"`
}

type destroySafetyModel struct {
	ProtectedPaths    types.List `tfsdk:"protected_paths"`
	ProtectedServices types.List `tfsdk:"protected_services"`
	ProtectedPackages types.List `tfsdk:"protected_packages"`
	ProtectedUsers    types.List `tfsdk:"protected_users"`
	ProtectedGroups   types.List `tfsdk:"protected_groups"`
}

// Metadata sets the provider type name.
func (p *BaseProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = p.config.Name
	resp.Version = p.config.Version
}

// Schema defines the provider-level configuration schema.
func (p *BaseProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: fmt.Sprintf("The %s provider manages system resources via an agentless executor over SSH.", p.config.Name),
		Blocks: map[string]schema.Block{
			"ssh": schema.SingleNestedBlock{
				Description: "Provider-level SSH connection defaults applied to all hosts.",
				Attributes: map[string]schema.Attribute{
					"user": schema.StringAttribute{
						Description: "SSH username for connecting to targets.",
						Optional:    true,
					},
					"private_key": schema.StringAttribute{
						Description: "PEM-encoded SSH private key.",
						Optional:    true,
						Sensitive:   true,
					},
					"certificate": schema.StringAttribute{
						Description: "PEM-encoded SSH signed certificate.",
						Optional:    true,
						Sensitive:   true,
					},
					"agent": schema.BoolAttribute{
						Description: "Use the SSH agent for authentication.",
						Optional:    true,
					},
					"known_hosts_file": schema.StringAttribute{
						Description: "Optional path to an OpenSSH known_hosts file. Entries are loaded into provider memory during Configure() and used as an additional host trust source alongside TOFU and state-pinned fingerprints.",
						Optional:    true,
					},
				},
			},
			"default_target": schema.SingleNestedBlock{
				Description: "Default transport target used when resources do not specify target attributes.",
				Attributes: map[string]schema.Attribute{
					"target": schema.StringAttribute{
						Description: "Target host or address.",
						Required:    true,
					},
					"port": schema.Int64Attribute{
						Description: "Target port. Defaults to 22 for ssh.",
						Optional:    true,
					},
					"transport": schema.StringAttribute{
						Description: "Target transport. The current provider surface supports ssh.",
						Optional:    true,
					},
				},
			},
			"destroy_safety": schema.SingleNestedBlock{
				Description: "Additional critical host objects that should be protected from destructive destroy operations.",
				Attributes: map[string]schema.Attribute{
					"protected_paths": schema.ListAttribute{
						Description: "Additional protected absolute paths. Values ending in / are treated as protected prefixes.",
						Optional:    true,
						ElementType: types.StringType,
					},
					"protected_services": schema.ListAttribute{
						Description: "Additional protected service or unit names.",
						Optional:    true,
						ElementType: types.StringType,
					},
					"protected_packages": schema.ListAttribute{
						Description: "Additional protected package names.",
						Optional:    true,
						ElementType: types.StringType,
					},
					"protected_users": schema.ListAttribute{
						Description: "Additional protected user names.",
						Optional:    true,
						ElementType: types.StringType,
					},
					"protected_groups": schema.ListAttribute{
						Description: "Additional protected group names.",
						Optional:    true,
						ElementType: types.StringType,
					},
				},
			},
		},
		Attributes: map[string]schema.Attribute{
			"encrypted_tunnel": schema.BoolAttribute{
				Description: "Whether to require an end-to-end encrypted provider-to-executor tunnel. Defaults to true.",
				Optional:    true,
			},
			"use_post_quantum_hashes": schema.BoolAttribute{
				Description: "Whether executor-side plugin verification should use the embedded SHAKE256 digest set instead of the conventional BLAKE3 set. Defaults to false.",
				Optional:    true,
			},
			"dual_plugin_verification": schema.BoolAttribute{
				Description: "Whether the executor should verify both the compressed .wasm.zst payload and the decompressed .wasm payload against its embedded manifest. Defaults to false.",
				Optional:    true,
			},
			"max_connections": schema.Int64Attribute{
				Description: "Maximum number of concurrent SSH connections in the pool.",
				Optional:    true,
			},
			"ssh_dial_timeout_seconds": schema.Int64Attribute{
				Description: "Timeout for each TCP dial attempt while opening an SSH transport connection. Defaults to 3 seconds.",
				Optional:    true,
			},
			"ssh_handshake_timeout_seconds": schema.Int64Attribute{
				Description: "Timeout for each SSH protocol handshake after TCP dial succeeds. Defaults to 10 seconds.",
				Optional:    true,
			},
			"ssh_connect_retry_attempts": schema.Int64Attribute{
				Description: "Maximum attempts for opening a new pooled SSH session before surfacing failure. Defaults to 120.",
				Optional:    true,
			},
			"ssh_connect_retry_initial_backoff_ms": schema.Int64Attribute{
				Description: "Initial retry backoff in milliseconds for opening a new pooled SSH session. Defaults to 500.",
				Optional:    true,
			},
			"ssh_connect_retry_max_backoff_ms": schema.Int64Attribute{
				Description: "Maximum retry backoff in milliseconds for opening a new pooled SSH session. Defaults to 3000.",
				Optional:    true,
			},
			"ssh_connect_retry_timeout_seconds": schema.Int64Attribute{
				Description: "Total timeout budget in seconds for opening a new pooled SSH session across retries. Defaults to 300.",
				Optional:    true,
			},
			"ssh_reconnect_retry_attempts": schema.Int64Attribute{
				Description: "Maximum attempts for reconnecting an existing pooled SSH session before surfacing failure. Defaults to 120.",
				Optional:    true,
			},
			"ssh_reconnect_retry_initial_backoff_ms": schema.Int64Attribute{
				Description: "Initial retry backoff in milliseconds for reconnecting an existing pooled SSH session. Defaults to 500.",
				Optional:    true,
			},
			"ssh_reconnect_retry_max_backoff_ms": schema.Int64Attribute{
				Description: "Maximum retry backoff in milliseconds for reconnecting an existing pooled SSH session. Defaults to 3000.",
				Optional:    true,
			},
			"ssh_reconnect_retry_timeout_seconds": schema.Int64Attribute{
				Description: "Total timeout budget in seconds for reconnecting an existing pooled SSH session across retries. Defaults to 60.",
				Optional:    true,
			},
			"host_lock_timeout_seconds": schema.Int64Attribute{
				Description: "Optional timeout for waiting on the cross-workspace per-host execution lock. When unset or 0, waits indefinitely.",
				Optional:    true,
			},
			"retry_attempts": schema.Int64Attribute{
				Description: "Maximum executor RPC attempts before surfacing failure to Terraform.",
				Optional:    true,
			},
			"retry_initial_backoff_ms": schema.Int64Attribute{
				Description: "Initial provider retry backoff in milliseconds for executor RPC failures.",
				Optional:    true,
			},
			"retry_max_backoff_ms": schema.Int64Attribute{
				Description: "Maximum provider retry backoff in milliseconds for executor RPC failures.",
				Optional:    true,
			},
		},
	}
}

// Configure parses the provider configuration and initializes the connection
// pool and executor manager.
func (p *BaseProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Build SSH config from provider block.
	sshConfig := &transport.SSHConfig{HostKeyTrust: transport.NewHostKeyTrustStore()}
	if config.SSH != nil {
		if !config.SSH.User.IsNull() {
			sshConfig.User = config.SSH.User.ValueString()
		}
		if !config.SSH.PrivateKey.IsNull() {
			sshConfig.PrivateKey = config.SSH.PrivateKey.ValueString()
		}
		if !config.SSH.Certificate.IsNull() {
			sshConfig.Certificate = config.SSH.Certificate.ValueString()
		}
		if !config.SSH.Agent.IsNull() {
			sshConfig.Agent = config.SSH.Agent.ValueBool()
		}
		if !config.SSH.KnownHostsFile.IsNull() {
			sshConfig.KnownHostsFile = strings.TrimSpace(config.SSH.KnownHostsFile.ValueString())
		}
	}
	if err := sshConfig.HostKeyTrust.LoadKnownHostsFile(sshConfig.KnownHostsFile); err != nil {
		resp.Diagnostics.AddError("Invalid SSH known_hosts file", err.Error())
		return
	}

	// Create connection pool.
	poolOptions := transport.ConnectionPoolOptions{}
	if !config.MaxConnections.IsNull() {
		poolOptions.MaxConnections = int(config.MaxConnections.ValueInt64())
	}
	if !config.SSHDialTimeoutSeconds.IsNull() {
		poolOptions.SSHDialTimeout = time.Duration(config.SSHDialTimeoutSeconds.ValueInt64()) * time.Second
	}
	if !config.SSHHandshakeTimeoutSeconds.IsNull() {
		poolOptions.SSHHandshakeTimeout = time.Duration(config.SSHHandshakeTimeoutSeconds.ValueInt64()) * time.Second
	}
	if !config.SSHConnectRetryAttempts.IsNull() {
		poolOptions.ConnectRetry.MaxAttempts = int(config.SSHConnectRetryAttempts.ValueInt64())
	}
	if !config.SSHConnectRetryInitialBackoffMs.IsNull() {
		poolOptions.ConnectRetry.InitialBackoff = time.Duration(config.SSHConnectRetryInitialBackoffMs.ValueInt64()) * time.Millisecond
	}
	if !config.SSHConnectRetryMaxBackoffMs.IsNull() {
		poolOptions.ConnectRetry.MaxBackoff = time.Duration(config.SSHConnectRetryMaxBackoffMs.ValueInt64()) * time.Millisecond
	}
	if !config.SSHConnectRetryTimeoutSeconds.IsNull() {
		poolOptions.ConnectRetry.TotalTimeout = time.Duration(config.SSHConnectRetryTimeoutSeconds.ValueInt64()) * time.Second
	}
	if !config.SSHReconnectRetryAttempts.IsNull() {
		poolOptions.ReconnectRetry.MaxAttempts = int(config.SSHReconnectRetryAttempts.ValueInt64())
	}
	if !config.SSHReconnectRetryInitialBackoffMs.IsNull() {
		poolOptions.ReconnectRetry.InitialBackoff = time.Duration(config.SSHReconnectRetryInitialBackoffMs.ValueInt64()) * time.Millisecond
	}
	if !config.SSHReconnectRetryMaxBackoffMs.IsNull() {
		poolOptions.ReconnectRetry.MaxBackoff = time.Duration(config.SSHReconnectRetryMaxBackoffMs.ValueInt64()) * time.Millisecond
	}
	if !config.SSHReconnectRetryTimeoutSeconds.IsNull() {
		poolOptions.ReconnectRetry.TotalTimeout = time.Duration(config.SSHReconnectRetryTimeoutSeconds.ValueInt64()) * time.Second
	}
	p.pool = transport.NewConnectionPoolWithOptions(sshConfig, poolOptions)

	// Create executor manager.
	p.manager = hostsession.NewExecutorManager(p.pool, p.config.Assets)
	policy, err := supportpolicy.Resolve(p.config.SupportPolicy)
	if err != nil {
		resp.Diagnostics.AddError("Invalid support policy", err.Error())
		return
	}
	p.manager.SetSupportPolicy(policy)
	encryptedTunnel := true
	if !config.EncryptedTunnel.IsNull() {
		encryptedTunnel = config.EncryptedTunnel.ValueBool()
	}
	p.manager.SetEncryptedTunnelEnabled(encryptedTunnel)
	if !config.UsePostQuantumHashes.IsNull() {
		p.manager.SetUsePostQuantumPluginDigests(config.UsePostQuantumHashes.ValueBool())
	}
	if !config.DualPluginVerification.IsNull() {
		p.manager.SetDualPluginVerification(config.DualPluginVerification.ValueBool())
	}
	if !config.HostLockTimeoutSeconds.IsNull() {
		p.manager.SetHostLockTimeout(time.Duration(config.HostLockTimeoutSeconds.ValueInt64()) * time.Second)
	}
	retryPolicy := hostsession.RetryPolicy{}
	if !config.RetryAttempts.IsNull() {
		retryPolicy.MaxAttempts = int(config.RetryAttempts.ValueInt64())
	}
	if !config.RetryInitialBackoffMs.IsNull() {
		retryPolicy.InitialBackoff = time.Duration(config.RetryInitialBackoffMs.ValueInt64()) * time.Millisecond
	}
	if !config.RetryMaxBackoffMs.IsNull() {
		retryPolicy.MaxBackoff = time.Duration(config.RetryMaxBackoffMs.ValueInt64()) * time.Millisecond
	}
	p.manager.SetRetryPolicy(retryPolicy)

	var defaultHost *transport.TransportConfig
	if config.DefaultTarget != nil && !config.DefaultTarget.Target.IsNull() {
		defaultHost = &transport.TransportConfig{
			Target: config.DefaultTarget.Target.ValueString(),
		}
		if !config.DefaultTarget.Port.IsNull() {
			defaultHost.Port = int(config.DefaultTarget.Port.ValueInt64())
		}
		if !config.DefaultTarget.Transport.IsNull() {
			defaultHost.Transport = config.DefaultTarget.Transport.ValueString()
		}
	}

	destroySafety := engine.DefaultDestroySafetyConfig()
	if config.DestroySafety != nil {
		destroySafety = engine.NewDestroySafetyConfig(
			listStringValues(ctx, config.DestroySafety.ProtectedPaths, &resp.Diagnostics),
			listStringValues(ctx, config.DestroySafety.ProtectedServices, &resp.Diagnostics),
			listStringValues(ctx, config.DestroySafety.ProtectedPackages, &resp.Diagnostics),
			listStringValues(ctx, config.DestroySafety.ProtectedUsers, &resp.Diagnostics),
			listStringValues(ctx, config.DestroySafety.ProtectedGroups, &resp.Diagnostics),
		)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	providerData := &engine.ProviderData{
		ExecutorMgr:   p.manager,
		Pool:          p.pool,
		DefaultHost:   defaultHost,
		DestroySafety: destroySafety,
	}

	// Make pool and manager available to resources and data sources via
	// the provider data field on the request context.
	resp.ResourceData = providerData
	resp.DataSourceData = providerData
	resp.ActionData = providerData
}

func listStringValues(ctx context.Context, value types.List, diagnostics *diag.Diagnostics) []string {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}

	var result []string
	diagnostics.Append(value.ElementsAs(ctx, &result, false)...)
	if diagnostics.HasError() {
		return nil
	}
	return result
}

// Resources returns all registered resource types.
func (p *BaseProvider) Resources(_ context.Context) []func() resource.Resource {
	result := make([]func() resource.Resource, len(p.resources))
	for i, factory := range p.resources {
		result[i] = factory
	}
	return result
}

// DataSources returns all registered data source types.
func (p *BaseProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	result := make([]func() datasource.DataSource, len(p.dataSources))
	for i, factory := range p.dataSources {
		result[i] = factory
	}
	return result
}

// Actions returns all registered action types.
func (p *BaseProvider) Actions(_ context.Context) []func() frameworkaction.Action {
	result := make([]func() frameworkaction.Action, len(p.actions))
	for i, factory := range p.actions {
		result[i] = factory
	}
	return result
}
