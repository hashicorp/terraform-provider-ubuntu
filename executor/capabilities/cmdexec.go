// Copyright IBM Corp. 2026

package capabilities

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/hashicorp/terraform-provider-ubuntu/executor/logging"
	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

const defaultCmdTimeout = 30 * time.Second

// CmdExec executes a command with arguments and returns the result.
func CmdExec(ctx context.Context, name string, args ...string) CmdResult {
	return cmdExecInput(ctx, nil, name, args...)
}

func cmdExecInput(ctx context.Context, input io.Reader, name string, args ...string) CmdResult {
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), defaultCmdTimeout)
		defer cancel()
	}

	originalName := name
	originalArgs := append([]string(nil), args...)
	execution, ok := ExecutionContextFromContext(ctx)
	var executionPtr *hostrpc.ExecutionContext
	if ok {
		executionCopy := execution
		executionPtr = &executionCopy
	}

	name, args = commandForExecution(ctx, name, args)
	started := time.Now()
	log.Printf("[cmdexec] start requested_name=%q requested_args=%s resolved_name=%q resolved_args=%s execution=%s", originalName, logging.SummarizeArgs(originalArgs), name, logging.SummarizeArgs(args), logging.SummarizeExecution(executionPtr))

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = input
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	result := CmdResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
			result.Stderr = fmt.Sprintf("%s\n%s", result.Stderr, err.Error())
		}
	}

	log.Printf("[cmdexec] complete duration=%s exit=%d stdout=%s stderr=%s", time.Since(started), result.ExitCode, logging.Preview(result.Stdout, 240), logging.Preview(result.Stderr, 240))

	return result
}

func commandForExecution(ctx context.Context, name string, args []string) (string, []string) {
	execution, ok := ExecutionContextFromContext(ctx)
	if !ok {
		return name, args
	}

	if execution.BecomeUser == "" && os.Geteuid() == 0 {
		return name, args
	}

	sudoArgs := []string{"-n"}
	if execution.BecomeUser != "" && execution.BecomeUser != "root" {
		sudoArgs = append(sudoArgs, "-u", execution.BecomeUser)
	}
	sudoArgs = append(sudoArgs, "--", name)
	sudoArgs = append(sudoArgs, args...)

	return "sudo", sudoArgs
}

// CmdExists checks if a command is available in PATH.
func CmdExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
