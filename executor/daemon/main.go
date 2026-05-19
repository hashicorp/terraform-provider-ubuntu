package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/hashicorp/terraform-provider-ubuntu/executor/capabilities"
	"github.com/hashicorp/terraform-provider-ubuntu/executor/logging"
	"github.com/hashicorp/terraform-provider-ubuntu/executor/runtime"
	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

func main() {
	serve := flag.Bool("serve", false, "Run in serve mode, reading JSON from stdin")
	encryptedTunnel := flag.Bool("encrypted-tunnel", false, "Require an end-to-end encrypted provider-to-executor tunnel on stdin/stdout")
	runRestartJournal := flag.String("run-restart-journal", "", "Run a detached restart operation journal helper")
	runFileStat := flag.String("run-file-stat", "", "Run a detached file stat helper")
	runFileDelete := flag.String("run-file-delete", "", "Run a detached file delete helper")
	runFileRead := flag.String("run-file-read", "", "Run a detached file read helper")
	runFileReadlink := flag.String("run-file-readlink", "", "Run a detached file readlink helper")
	runFileRename := flag.String("run-file-rename", "", "Run a detached file rename helper")
	runFileRenameTo := flag.String("run-file-rename-to", "", "Destination path for detached file rename helper")
	runFileSymlink := flag.String("run-file-symlink", "", "Run a detached symlink helper")
	runFileSymlinkTarget := flag.String("run-file-symlink-target", "", "Target path for detached symlink helper")
	runFileWrite := flag.String("run-file-write", "", "Run a detached file write helper")
	runFileWriteMode := flag.String("run-file-write-mode", "", "Permissions for detached file write helper, in octal")
	runDirRead := flag.String("run-dir-read", "", "Run a detached directory read helper")
	runDirEnsure := flag.String("run-dir-ensure", "", "Run a detached directory creation helper")
	runDirEnsureMode := flag.String("run-dir-ensure-mode", "", "Permissions for detached directory creation helper, in octal")
	flag.Parse()

	logPath, logCloser, err := logging.Configure()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configure executor logging: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		if logCloser != nil {
			_ = logCloser.Close()
		}
	}()

	if *runRestartJournal != "" {
		if err := runtime.RunJournaledRestart(*runRestartJournal); err != nil {
			fmt.Fprintf(os.Stderr, "restart journal failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *runFileStat != "" {
		if err := capabilities.WriteFileStat(*runFileStat, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "file stat failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *runFileDelete != "" {
		if err := capabilities.FileDelete(*runFileDelete); err != nil {
			fmt.Fprintf(os.Stderr, "file delete failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *runFileRead != "" {
		if err := capabilities.WriteFileContents(*runFileRead, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "file read failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *runFileReadlink != "" {
		if err := capabilities.WriteReadlinkTarget(*runFileReadlink, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "file readlink failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *runFileRename != "" {
		if err := capabilities.FileRename(*runFileRename, *runFileRenameTo); err != nil {
			fmt.Fprintf(os.Stderr, "file rename failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *runFileSymlink != "" {
		if err := capabilities.FileSymlink(*runFileSymlinkTarget, *runFileSymlink); err != nil {
			fmt.Fprintf(os.Stderr, "file symlink failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *runFileWrite != "" {
		mode, err := parseHelperMode(*runFileWriteMode)
		if err != nil {
			fmt.Fprintf(os.Stderr, "file write failed: %v\n", err)
			os.Exit(1)
		}
		if err := capabilities.WriteFileFromReader(*runFileWrite, os.Stdin, mode); err != nil {
			fmt.Fprintf(os.Stderr, "file write failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *runDirRead != "" {
		if err := capabilities.WriteDirEntries(*runDirRead, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "directory read failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *runDirEnsure != "" {
		mode, err := parseHelperMode(*runDirEnsureMode)
		if err != nil {
			fmt.Fprintf(os.Stderr, "directory ensure failed: %v\n", err)
			os.Exit(1)
		}
		if err := capabilities.DirEnsure(*runDirEnsure, mode); err != nil {
			fmt.Fprintf(os.Stderr, "directory ensure failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if !*serve {
		fmt.Fprintln(os.Stderr, "Usage: executor --serve | --run-restart-journal <path> | --run-file-stat <path> | --run-file-delete <path> | --run-file-read <path> | --run-file-readlink <path> | --run-file-rename <from> --run-file-rename-to <to> | --run-file-symlink <path> --run-file-symlink-target <target> | --run-file-write <path> --run-file-write-mode <mode> | --run-dir-read <path> | --run-dir-ensure <path> --run-dir-ensure-mode <mode>")
		os.Exit(1)
	}

	log.Printf("Executor startup pid=%d serve=%t encrypted_tunnel=%t log_path=%s", os.Getpid(), *serve, *encryptedTunnel, logPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals for clean shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		log.Println("Signal received, shutting down")
		cancel()
	}()

	// Run host discovery.
	log.Println("Running host discovery...")
	profile := capabilities.Discover()
	log.Printf("Discovery complete: %s %s (%s)", profile.DistroID, profile.DistroVersion, profile.DistroFamily)

	// Initialize the WASM runtime.
	hostAPI := capabilities.NewHostAPI(profile)
	wasmRT, err := runtime.NewWASMRuntime(ctx, hostAPI)
	if err != nil {
		log.Fatalf("Failed to initialize WASM runtime: %v", err)
	}
	defer wasmRT.Close()

	dispatcher := runtime.NewDispatcher(wasmRT)
	mux := handler.Map{
		hostrpc.MethodExecutorDiscover: handler.New(func(context.Context) (interface{}, error) {
			return profile, nil
		}),
		hostrpc.MethodExecutorShutdown: handler.New(func(context.Context) error {
			log.Println("Shutdown requested")
			cancel()
			return nil
		}),
		hostrpc.MethodJournalConfigure: handler.New(func(_ context.Context, params hostrpc.JournalConfigureParams) error {
			return dispatcher.ConfigureJournal(params)
		}),
		hostrpc.MethodJournalOperationAcquire: handler.New(func(ctx context.Context, params hostrpc.OperationAcquireParams) (*hostrpc.OperationAcquireResult, error) {
			return dispatcher.AcquireOperation(ctx, params)
		}),
		hostrpc.MethodJournalOperationRelease: handler.New(func(_ context.Context, params hostrpc.OperationReleaseParams) error {
			return dispatcher.ReleaseOperation(params)
		}),
		hostrpc.MethodJournalRebootPrepare: handler.New(func(_ context.Context, params hostrpc.RebootJournalPrepareParams) (*hostrpc.RebootJournalEntry, error) {
			return dispatcher.PrepareRebootJournal(params)
		}),
		hostrpc.MethodJournalRebootMarkPhase: handler.New(func(_ context.Context, params hostrpc.RebootJournalMarkPhaseParams) (*hostrpc.RebootJournalEntry, error) {
			return dispatcher.MarkRebootJournalPhase(params)
		}),
		hostrpc.MethodJournalRebootMarkFailed: handler.New(func(_ context.Context, params hostrpc.RebootJournalMarkFailedParams) (*hostrpc.RebootJournalEntry, error) {
			return dispatcher.MarkRebootJournalFailed(params)
		}),
		hostrpc.MethodJournalRebootMarkCompleted: handler.New(func(_ context.Context, params hostrpc.RebootJournalMarkCompletedParams) (*hostrpc.RebootJournalEntry, error) {
			return dispatcher.MarkRebootJournalCompleted(params)
		}),
		hostrpc.MethodHostCommand: handler.New(func(ctx context.Context, params hostrpc.HostCommandParams) (*hostrpc.CommandResult, error) {
			return dispatcher.HostCommand(ctx, params)
		}),
		hostrpc.MethodActionInvoke: handler.New(func(ctx context.Context, params hostrpc.ActionInvokeParams) (*hostrpc.OperationResult, error) {
			return dispatcher.InvokeAction(ctx, params)
		}),
		hostrpc.MethodActionRestart: handler.New(func(ctx context.Context, params hostrpc.RestartProcessParams) (*hostrpc.CommandResult, error) {
			return dispatcher.RestartProcess(ctx, params)
		}),
		hostrpc.MethodModuleLoad: handler.New(func(_ context.Context, params hostrpc.ModuleLoadParams) (*hostrpc.ModuleLoadResult, error) {
			return dispatcher.LoadModule(params)
		}),
		hostrpc.MethodResourceValidate: handler.New(func(ctx context.Context, params hostrpc.ResourceValidateParams) (*hostrpc.OperationResult, error) {
			return dispatcher.ValidateResource(ctx, params)
		}),
		hostrpc.MethodResourceRead: handler.New(func(ctx context.Context, params hostrpc.ResourceReadParams) (*hostrpc.OperationResult, error) {
			return dispatcher.ReadResource(ctx, params)
		}),
		hostrpc.MethodResourceCreate: handler.New(func(ctx context.Context, params hostrpc.ResourceCreateParams) (*hostrpc.OperationResult, error) {
			return dispatcher.CreateResource(ctx, params)
		}),
		hostrpc.MethodResourceUpdate: handler.New(func(ctx context.Context, params hostrpc.ResourceUpdateParams) (*hostrpc.OperationResult, error) {
			return dispatcher.UpdateResource(ctx, params)
		}),
		hostrpc.MethodResourceDelete: handler.New(func(ctx context.Context, params hostrpc.ResourceDeleteParams) (*hostrpc.OperationResult, error) {
			return dispatcher.DeleteResource(ctx, params)
		}),
		hostrpc.MethodResourceImport: handler.New(func(ctx context.Context, params hostrpc.ResourceImportParams) (*hostrpc.OperationResult, error) {
			return dispatcher.ImportResource(ctx, params)
		}),
		hostrpc.MethodDataSourceRead: handler.New(func(ctx context.Context, params hostrpc.DataSourceReadParams) (*hostrpc.OperationResult, error) {
			return dispatcher.ReadDataSource(ctx, params)
		}),
	}

	rpcChannel, err := hostrpc.NewServerChannel(os.Stdin, os.Stdout, hostrpc.ChannelOptions{EncryptedTunnel: *encryptedTunnel})
	if err != nil {
		log.Fatalf("Failed to establish executor RPC channel: %v", err)
	}

	server := jrpc2.NewServer(mux, &jrpc2.ServerOptions{Concurrency: 8}).Start(rpcChannel)
	go func() {
		<-ctx.Done()
		server.Stop()
	}()

	if err := server.Wait(); err != nil {
		log.Printf("RPC server exited: %v", err)
		os.Exit(1)
	}
}

func parseHelperMode(raw string) (os.FileMode, error) {
	if raw == "" {
		return 0, fmt.Errorf("missing required mode")
	}

	parsed, err := strconv.ParseUint(raw, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("parse mode %q: %w", raw, err)
	}
	return os.FileMode(parsed), nil
}
