package capabilities

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

func TestCommandForExecutionWithoutPrivilege(t *testing.T) {
	t.Parallel()

	name, args := commandForExecution(context.Background(), "echo", []string{"hello"})
	if name != "echo" {
		t.Fatalf("expected direct command, got %q", name)
	}
	if !reflect.DeepEqual(args, []string{"hello"}) {
		t.Fatalf("unexpected args: %#v", args)
	}
}

func TestCommandForExecutionWithRootPrivilege(t *testing.T) {
	t.Parallel()

	ctx := WithExecutionContext(context.Background(), &hostrpc.ExecutionContext{Become: true})
	name, args := commandForExecution(ctx, "echo", []string{"hello"})
	if name != "sudo" {
		t.Fatalf("expected sudo wrapper, got %q", name)
	}
	if !reflect.DeepEqual(args, []string{"-n", "--", "echo", "hello"}) {
		t.Fatalf("unexpected args: %#v", args)
	}
}

func TestCommandForExecutionWithUserPrivilege(t *testing.T) {
	t.Parallel()

	ctx := WithExecutionContext(context.Background(), &hostrpc.ExecutionContext{Become: true, BecomeUser: "deploy"})
	name, args := commandForExecution(ctx, "echo", []string{"hello"})
	if name != "sudo" {
		t.Fatalf("expected sudo wrapper, got %q", name)
	}
	if !reflect.DeepEqual(args, []string{"-n", "-u", "deploy", "--", "echo", "hello"}) {
		t.Fatalf("unexpected args: %#v", args)
	}
}
