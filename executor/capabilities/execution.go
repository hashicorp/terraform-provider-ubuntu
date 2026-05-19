package capabilities

import (
	"context"

	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

type executionContextKey struct{}

func WithExecutionContext(ctx context.Context, execution *hostrpc.ExecutionContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if execution == nil || !execution.Become {
		return ctx
	}

	copy := *execution
	return context.WithValue(ctx, executionContextKey{}, copy)
}

func ExecutionContextFromContext(ctx context.Context) (hostrpc.ExecutionContext, bool) {
	if ctx == nil {
		return hostrpc.ExecutionContext{}, false
	}

	execution, ok := ctx.Value(executionContextKey{}).(hostrpc.ExecutionContext)
	if !ok || !execution.Become {
		return hostrpc.ExecutionContext{}, false
	}

	return execution, true
}
