// Copyright IBM Corp. 2026

package engine

import (
	"context"
	"time"
)

const defaultReadOperationTimeout = 30 * time.Second

func withReadOperationTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, defaultReadOperationTimeout)
}
