// Copyright IBM Corp. 2026

package transport

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const transportLogSubsystem = "transport"

func transportLogDebug(ctx context.Context, msg string, config TransportConfig, fields map[string]interface{}) {
	transportLog(ctx, msg, config, fields, false)
}

func transportLogWarn(ctx context.Context, msg string, config TransportConfig, fields map[string]interface{}) {
	transportLog(ctx, msg, config, fields, true)
}

func transportLog(ctx context.Context, msg string, config TransportConfig, fields map[string]interface{}, warn bool) {
	ctx = tflog.NewSubsystem(ctx, transportLogSubsystem, tflog.WithAdditionalLocationOffset(1))

	merged := map[string]interface{}{
		"target":    config.NormalizedTarget(),
		"port":      config.ResolvedPort(),
		"transport": config.NormalizedTransport(),
		"endpoint":  config.DisplayTarget(),
	}
	if user := strings.TrimSpace(config.SSHUser); user != "" {
		merged["ssh_user"] = user
	}
	for key, value := range fields {
		merged[key] = value
	}

	if warn {
		tflog.SubsystemWarn(ctx, transportLogSubsystem, msg, merged)
		return
	}
	tflog.SubsystemDebug(ctx, transportLogSubsystem, msg, merged)
}
