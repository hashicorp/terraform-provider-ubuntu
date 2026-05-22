package catalog

import (
	"strings"

	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"

	linuxfilescontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/linuxfiles"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/hostsession"
	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

func LinuxFiles() Fragment {
	kernelModulesContract := linuxfilescontract.KernelModulesResourceSchema()
	swapContract := linuxfilescontract.SwapResourceSchema()
	hostsEntryContract := linuxfilescontract.HostsEntryResourceSchema()
	sshdConfigContract := linuxfilescontract.SSHDConfigResourceSchema()
	sysctlEntryContract := linuxfilescontract.SysctlEntryResourceSchema()
	fstabEntryContract := linuxfilescontract.FstabEntryResourceSchema()
	crontabEntryContract := linuxfilescontract.CrontabEntryResourceSchema()
	fileContract := linuxfilescontract.FileResourceSchema()
	validationContract := fileContract.Blocks[linuxfilescontract.ValidationBlockName]
	symlinkContract := linuxfilescontract.SymlinkResourceSchema()
	fileInfoContract := linuxfilescontract.FileInfoDataSourceSchema()

	return Fragment{
		ID:             "linux_files",
		Scope:          "linux",
		RuntimeModules: []string{ModuleLinuxFiles},
		Resources: []engine.ResourceDefinition{
			{
				TypeName:          "kernel_modules",
				Pattern:           engine.PatternConfig,
				RequiredPrivilege: "root",
				RuntimeType:       "kernel_modules",
				RuntimeModule:     ModuleLinuxFiles,
				LockPlanner:       kernelModulesLockPlanner,
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: false,
					PreWriteValidation:   true,
				},
				DestroySafety: engine.DestroySafetyPolicy{
					Mode:  engine.DestroySafetyModeCriticalObject,
					Guard: protectedPathGuard("path", "id"),
				},
				Attributes: resourceAttributesFromPluginContract(kernelModulesContract.Attributes, nil),
			},
			{
				TypeName:          "swap",
				Pattern:           engine.PatternConfig,
				RequiredPrivilege: "root",
				RuntimeType:       "swap",
				RuntimeModule:     ModuleLinuxFiles,
				LockPlanner:       swapLockPlanner,
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: false,
					PreWriteValidation:   true,
				},
				DestroySafety: engine.DestroySafetyPolicy{
					Mode: engine.DestroySafetyModeNone,
				},
				Attributes: resourceAttributesFromPluginContract(swapContract.Attributes, nil),
			},
			{
				TypeName:                 "hosts_entry",
				Pattern:                  engine.PatternEntry,
				RequiredPrivilege:        "root",
				RuntimeType:              "hosts_entry",
				RuntimeModule:            ModuleLinuxFiles,
				LockPlanner:              hostsEntryLockPlanner,
				ImportRequiredOnExisting: true,
				ImportIdentity: joinedStringImportIdentity("/",
					importIdentityField{Key: "ip", Description: "IP address of the hosts entry to import."},
					importIdentityField{Key: "hostname", Description: "Primary hostname of the hosts entry to import."},
				),
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: false,
					PreWriteValidation:   true,
				},
				DestroySafety: engine.DestroySafetyPolicy{
					Mode:  engine.DestroySafetyModeCriticalObject,
					Guard: protectedHostsEntryGuard,
				},
				Attributes: resourceAttributesFromPluginContract(hostsEntryContract.Attributes, nil),
			},
			{
				TypeName:          "sshd_config",
				Pattern:           engine.PatternConfig,
				RequiredPrivilege: "root",
				RuntimeType:       "sshd_config",
				RuntimeModule:     ModuleLinuxFiles,
				LockPlanner:       sshdConfigLockPlanner,
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: false,
					PreWriteValidation:   true,
					ReadAfterWrite:       true,
				},
				DestroySafety: engine.DestroySafetyPolicy{
					Mode:  engine.DestroySafetyModeCriticalObject,
					Guard: protectedFixedPathGuard("/etc/ssh/sshd_config"),
				},
				Attributes: resourceAttributesFromPluginContract(sshdConfigContract.Attributes, nil),
			},
			{
				TypeName:                 "sysctl_entry",
				Pattern:                  engine.PatternEntry,
				RequiredPrivilege:        "root",
				RuntimeType:              "sysctl_entry",
				RuntimeModule:            ModuleLinuxFiles,
				LockPlanner:              sysctlEntryLockPlanner,
				ImportRequiredOnExisting: true,
				ImportIdentity: singleStringImportIdentity(
					importIdentityField{Key: "key", Description: "Sysctl key to import."},
				),
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: false,
					PreWriteValidation:   true,
				},
				DestroySafety: engine.DestroySafetyPolicy{
					Mode:  engine.DestroySafetyModeCriticalObject,
					Guard: protectedSysctlGuard,
				},
				Attributes: resourceAttributesFromPluginContract(sysctlEntryContract.Attributes, nil),
			},
			{
				TypeName:                 "fstab_entry",
				Pattern:                  engine.PatternEntry,
				RequiredPrivilege:        "root",
				RuntimeType:              "fstab_entry",
				RuntimeModule:            ModuleLinuxFiles,
				LockPlanner:              fstabEntryLockPlanner,
				ImportRequiredOnExisting: true,
				ImportIdentity: singleStringImportIdentity(
					importIdentityField{Key: "mount", Description: "Mount path of the fstab entry to import."},
				),
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: false,
					PreWriteValidation:   true,
				},
				DestroySafety: engine.DestroySafetyPolicy{
					Mode:  engine.DestroySafetyModeCriticalObject,
					Guard: protectedFstabGuard,
				},
				Attributes: resourceAttributesFromPluginContract(fstabEntryContract.Attributes, nil),
			},
			{
				TypeName:          "crontab_entry",
				Pattern:           engine.PatternEntry,
				RequiredPrivilege: "root",
				RuntimeType:       "crontab_entry",
				RuntimeModule:     ModuleLinuxFiles,
				LockPlanner:       crontabEntryLockPlanner,
				ImportIdentity: joinedStringImportIdentity("/",
					importIdentityField{Key: "user", Description: "User that owns the crontab entry to import."},
					importIdentityField{Key: "name", Description: "Stable tf-linux-provider name of the managed crontab entry to import."},
				),
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: false,
					PreWriteValidation:   true,
				},
				Attributes: resourceAttributesFromPluginContract(crontabEntryContract.Attributes, nil),
			},
			{
				TypeName:          "file",
				Pattern:           engine.PatternFile,
				RequiredPrivilege: "dynamic",
				RuntimeType:       "file",
				RuntimeModule:     ModuleLinuxFiles,
				LockPlanner:       fileLockPlanner,
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: false,
					PreWriteValidation:   true,
				},
				ImportRequiredOnExisting: true,
				ImportIdentity: singleStringImportIdentity(
					importIdentityField{Key: "path", Description: "Absolute path of the file to import."},
				),
				DestroySafety: engine.DestroySafetyPolicy{
					Mode:  engine.DestroySafetyModeCriticalObject,
					Guard: protectedPathGuard("path", "id"),
				},
				Attributes: resourceAttributesFromPluginContract(fileContract.Attributes, map[string]resourceschema.Attribute{
					"path": resourceschema.StringAttribute{
						Description: fileContract.Attributes["path"].Description,
						Required:    true,
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.RequiresReplace(),
						},
					},
					"content":        resourceschema.StringAttribute{Description: fileContract.Attributes["content"].Description, Optional: true, Sensitive: true, CustomType: engine.DigestedStringType{}},
					"content_base64": resourceschema.StringAttribute{Description: fileContract.Attributes["content_base64"].Description, Optional: true, Sensitive: true, CustomType: engine.DigestedBase64StringType{}},
					"sensitive":      resourceschema.BoolAttribute{Description: fileContract.Attributes["sensitive"].Description, Optional: true, Computed: true, Default: booldefault.StaticBool(true)},
					"owner":          resourceschema.StringAttribute{Description: fileContract.Attributes["owner"].Description, Optional: true, Computed: true, Default: stringdefault.StaticString("root")},
					"group":          resourceschema.StringAttribute{Description: fileContract.Attributes["group"].Description, Optional: true, Computed: true, Default: stringdefault.StaticString("root")},
					"mode":           resourceschema.StringAttribute{Description: fileContract.Attributes["mode"].Description, Optional: true, Computed: true, Default: stringdefault.StaticString("0644")},
				}),
				Blocks: resourceBlocksFromPluginContract(fileContract.Blocks, map[string]resourceschema.Block{
					linuxfilescontract.ValidationBlockName: resourceschema.SingleNestedBlock{
						Description: validationContract.Description,
						Attributes: resourceAttributesFromPluginContract(validationContract.Attributes, map[string]resourceschema.Attribute{
							linuxfilescontract.ValidationInPlaceAttributeName: resourceschema.BoolAttribute{
								Description: validationContract.Attributes[linuxfilescontract.ValidationInPlaceAttributeName].Description,
								Optional:    true,
								Computed:    true,
								Default:     booldefault.StaticBool(false),
							},
							linuxfilescontract.ValidationFileAsArgAttributeName: resourceschema.BoolAttribute{
								Description: validationContract.Attributes[linuxfilescontract.ValidationFileAsArgAttributeName].Description,
								Optional:    true,
								Computed:    true,
								Default:     booldefault.StaticBool(true),
							},
						}),
					},
				}),
			},
			{
				TypeName:          "symlink",
				Pattern:           engine.PatternFile,
				RequiredPrivilege: "dynamic",
				RuntimeType:       "symlink",
				RuntimeModule:     ModuleLinuxFiles,
				LockPlanner:       fileLockPlanner,
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: false,
					PreWriteValidation:   true,
				},
				ImportRequiredOnExisting: true,
				ImportIdentity: singleStringImportIdentity(
					importIdentityField{Key: "path", Description: "Absolute path of the symlink to import."},
				),
				DestroySafety: engine.DestroySafetyPolicy{
					Mode:  engine.DestroySafetyModeCriticalObject,
					Guard: protectedPathGuard("path", "id"),
				},
				Attributes: resourceAttributesFromPluginContract(symlinkContract.Attributes, map[string]resourceschema.Attribute{
					"path": resourceschema.StringAttribute{
						Description: symlinkContract.Attributes["path"].Description,
						Required:    true,
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.RequiresReplace(),
						},
					},
				}),
			},
		},
		DataSources: []engine.DataSourceDefinition{
			{
				TypeName:      "file_info",
				RuntimeType:   "file_info",
				RuntimeModule: ModuleLinuxFiles,
				LockPlanner:   fileInfoLockPlanner,
				ExecutionPolicy: func(_ string, op *hostsession.OperationMessage) (*hostrpc.ExecutionContext, error) {
					if op == nil || op.Config == nil {
						return nil, nil
					}
					runAs, _ := op.Config["run_as"].(string)
					runAs = strings.TrimSpace(runAs)
					if runAs == "" {
						return nil, nil
					}
					return &hostrpc.ExecutionContext{Become: true, BecomeUser: runAs}, nil
				},
				Attributes: dataSourceAttributesFromPluginContract(fileInfoContract.Attributes, nil),
			},
		},
	}
}
