// Copyright IBM Corp. 2026

package linuxfiles

import (
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
)

const (
	ValidationBlockName              = "validation"
	ValidationArgvAttributeName      = "argv"
	ValidationInPlaceAttributeName   = "in_place"
	ValidationFileAsArgAttributeName = "file_as_arg"
)

type FileValidation struct {
	Argv      []string
	InPlace   bool
	FileAsArg bool
}

func KernelModulesResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"path":    {Type: pluginsdk.AttrString, Required: true, Description: "Absolute path to the modules-load configuration file."},
			"modules": {Type: pluginsdk.AttrList, Required: true, Description: "Kernel modules to load now and on subsequent boots."},
		},
	}
}

func SwapResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"enabled": {Type: pluginsdk.AttrBool, Required: true, Description: "Whether system swap should be enabled at runtime and restored from managed /etc/fstab entries."},
		},
	}
}

func HostsEntryResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"ip":       {Type: pluginsdk.AttrString, Required: true, Description: "IP address for the hosts entry."},
			"hostname": {Type: pluginsdk.AttrString, Required: true, Description: "Primary hostname for the hosts entry."},
			"aliases":  {Type: pluginsdk.AttrList, Optional: true, Description: "Optional list of alias hostnames."},
			"comment":  {Type: pluginsdk.AttrString, Optional: true, Description: "Optional comment for the hosts entry."},
		},
	}
}

func SSHDConfigResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"port":                    {Type: pluginsdk.AttrInt, Optional: true, Computed: true, Description: "SSH listen port."},
			"permit_root_login":       {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Whether root login is permitted (yes, no, prohibit-password, forced-commands-only)."},
			"password_authentication": {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Whether password authentication is enabled (yes or no)."},
			"pubkey_authentication":   {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Whether public key authentication is enabled (yes or no)."},
			"max_auth_tries":          {Type: pluginsdk.AttrInt, Optional: true, Computed: true, Description: "Maximum number of authentication attempts per connection."},
			"x11_forwarding":          {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Whether X11 forwarding is enabled (yes or no)."},
			"allow_users":             {Type: pluginsdk.AttrList, Optional: true, Description: "List of usernames allowed to log in via SSH."},
			"allow_groups":            {Type: pluginsdk.AttrList, Optional: true, Description: "List of groups allowed to log in via SSH."},
			"client_alive_interval":   {Type: pluginsdk.AttrInt, Optional: true, Computed: true, Description: "Client alive interval in seconds."},
			"client_alive_count_max":  {Type: pluginsdk.AttrInt, Optional: true, Computed: true, Description: "Maximum number of client alive messages without response."},
		},
	}
}

func SysctlEntryResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"key":   {Type: pluginsdk.AttrString, Required: true, Description: "Sysctl key to manage."},
			"value": {Type: pluginsdk.AttrString, Required: true, Description: "Desired sysctl value."},
		},
	}
}

func FstabEntryResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"device":  {Type: pluginsdk.AttrString, Required: true, Description: "Block device or other mount source."},
			"mount":   {Type: pluginsdk.AttrString, Required: true, Description: "Mount point path."},
			"fstype":  {Type: pluginsdk.AttrString, Required: true, Description: "Filesystem type."},
			"options": {Type: pluginsdk.AttrList, Optional: true, Description: "Mount options."},
			"dump":    {Type: pluginsdk.AttrInt, Optional: true, Computed: true, Description: "Dump field value."},
			"passno":  {Type: pluginsdk.AttrInt, Optional: true, Computed: true, Description: "Filesystem check order."},
		},
	}
}

func CrontabEntryResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"name":         {Type: pluginsdk.AttrString, Required: true, Description: "Stable name for this managed crontab entry."},
			"user":         {Type: pluginsdk.AttrString, Required: true, Description: "User whose per-user crontab should contain this entry."},
			"command":      {Type: pluginsdk.AttrString, Required: true, Description: "Shell command run by cron."},
			"schedule":     {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Cron schedule as either a five-field expression or an @special token."},
			"minute":       {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Minute field for a five-field cron schedule."},
			"hour":         {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Hour field for a five-field cron schedule."},
			"day_of_month": {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Day-of-month field for a five-field cron schedule."},
			"month":        {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Month field for a five-field cron schedule."},
			"day_of_week":  {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Day-of-week field for a five-field cron schedule."},
		},
	}
}

func FileInfoDataSourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"path":       {Type: pluginsdk.AttrString, Required: true, Description: "Absolute path to query."},
			"run_as":     {Type: pluginsdk.AttrString, Optional: true, Description: "Run file metadata reads as this user."},
			"exists":     {Type: pluginsdk.AttrBool, Computed: true, Description: "Whether the file exists."},
			"size":       {Type: pluginsdk.AttrInt, Computed: true, Description: "File size in bytes."},
			"mode":       {Type: pluginsdk.AttrString, Computed: true, Description: "File permission mode."},
			"owner":      {Type: pluginsdk.AttrString, Computed: true, Description: "File owner."},
			"group":      {Type: pluginsdk.AttrString, Computed: true, Description: "File group."},
			"digest":     {Type: pluginsdk.AttrString, Computed: true, Description: "Content digest, including the algorithm tag."},
			"mtime_unix": {Type: pluginsdk.AttrInt, Computed: true, Description: "Last modification time as a Unix timestamp in seconds."},
		},
	}
}

func FileResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"path": {
				Type:        pluginsdk.AttrString,
				Required:    true,
				Description: "Absolute path to the file on the target.",
			},
			"content": {
				Type:        pluginsdk.AttrString,
				Optional:    true,
				Sensitive:   true,
				Description: "File content as a UTF-8 string.",
			},
			"content_base64": {
				Type:        pluginsdk.AttrString,
				Optional:    true,
				Sensitive:   true,
				Description: "File content as base64-encoded bytes.",
			},
			"sensitive": {
				Type:        pluginsdk.AttrBool,
				Optional:    true,
				Computed:    true,
				Description: "Whether file content should be stored as a digest in Terraform state.",
			},
			"owner": {
				Type:        pluginsdk.AttrString,
				Optional:    true,
				Computed:    true,
				Description: "File owner.",
			},
			"group": {
				Type:        pluginsdk.AttrString,
				Optional:    true,
				Computed:    true,
				Description: "File group.",
			},
			"mode": {
				Type:        pluginsdk.AttrString,
				Optional:    true,
				Computed:    true,
				Description: "File permission mode (e.g. 0644).",
			},
			"digest": {
				Type:        pluginsdk.AttrString,
				Computed:    true,
				Description: "Content digest of the file, including the algorithm tag.",
			},
			"run_as": {
				Type:        pluginsdk.AttrString,
				Optional:    true,
				Description: "Run file operations as this user.",
			},
		},
		Blocks: map[string]pluginsdk.Block{
			ValidationBlockName: {
				Kind:        pluginsdk.BlockSingleNested,
				Description: "Optional validation command run against the staged or target file before the managed file is committed.",
				Attributes: map[string]pluginsdk.Attribute{
					ValidationArgvAttributeName: {
						Type:        pluginsdk.AttrList,
						Optional:    true,
						Description: "Validator command and fixed arguments.",
					},
					ValidationInPlaceAttributeName: {
						Type:        pluginsdk.AttrBool,
						Optional:    true,
						Computed:    true,
						Description: "Whether validation must run with the candidate content installed at the target path before rollback on failure.",
					},
					ValidationFileAsArgAttributeName: {
						Type:        pluginsdk.AttrBool,
						Optional:    true,
						Computed:    true,
						Description: "Whether the provider should append the staged or target file path as the final validator argument.",
					},
				},
			},
		},
	}
}

func SymlinkResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"path": {
				Type:        pluginsdk.AttrString,
				Required:    true,
				Description: "Absolute path to the symlink on the target.",
			},
			"target": {
				Type:        pluginsdk.AttrString,
				Required:    true,
				Description: "Symlink target path or relative reference.",
			},
			"run_as": {
				Type:        pluginsdk.AttrString,
				Optional:    true,
				Description: "Run symlink operations as this user.",
			},
		},
	}
}

func ParseFileValidation(raw interface{}) (*FileValidation, error) {
	if raw == nil {
		return nil, nil
	}

	validationMap, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("validation must be an object")
	}

	argvValue, ok := validationMap[ValidationArgvAttributeName]
	if !ok {
		return nil, fmt.Errorf("validation.%s must be set", ValidationArgvAttributeName)
	}
	argv, ok := asArgv(argvValue)
	if !ok || len(argv) == 0 {
		return nil, fmt.Errorf("validation.%s must contain at least one non-empty argument", ValidationArgvAttributeName)
	}
	for _, arg := range argv {
		if strings.TrimSpace(arg) == "" {
			return nil, fmt.Errorf("validation.%s must not contain empty arguments", ValidationArgvAttributeName)
		}
	}

	inPlace := false
	if rawInPlace, ok := validationMap[ValidationInPlaceAttributeName]; ok && rawInPlace != nil {
		boolValue, ok := rawInPlace.(bool)
		if !ok {
			return nil, fmt.Errorf("validation.%s must be a bool", ValidationInPlaceAttributeName)
		}
		inPlace = boolValue
	}

	fileAsArg := true
	if rawFileAsArg, ok := validationMap[ValidationFileAsArgAttributeName]; ok && rawFileAsArg != nil {
		boolValue, ok := rawFileAsArg.(bool)
		if !ok {
			return nil, fmt.Errorf("validation.%s must be a bool", ValidationFileAsArgAttributeName)
		}
		fileAsArg = boolValue
	}

	return &FileValidation{Argv: argv, InPlace: inPlace, FileAsArg: fileAsArg}, nil
}

func (v *FileValidation) StateValue() map[string]interface{} {
	if v == nil {
		return nil
	}
	return map[string]interface{}{
		ValidationArgvAttributeName:      append([]string(nil), v.Argv...),
		ValidationInPlaceAttributeName:   v.InPlace,
		ValidationFileAsArgAttributeName: v.FileAsArg,
	}
}

func asArgv(value interface{}) ([]string, bool) {
	switch value := value.(type) {
	case []string:
		out := make([]string, len(value))
		copy(out, value)
		return out, true
	case []interface{}:
		out := make([]string, 0, len(value))
		for _, item := range value {
			str, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, str)
		}
		return out, true
	default:
		return nil, false
	}
}
