package main

import (
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
	linuxfilescontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/linuxfiles"
)

func TestFileSchemaIncludesValidationBlock(t *testing.T) {
	t.Parallel()

	schema := (&fileResource{}).Schema()
	validation, ok := schema.Blocks[linuxfilescontract.ValidationBlockName]
	if !ok {
		t.Fatal("expected file schema to expose validation block")
	}
	if validation.Kind != pluginsdk.BlockSingleNested {
		t.Fatalf("validation block kind = %q, want %q", validation.Kind, pluginsdk.BlockSingleNested)
	}
	for _, name := range []string{
		linuxfilescontract.ValidationArgvAttributeName,
		linuxfilescontract.ValidationInPlaceAttributeName,
		linuxfilescontract.ValidationFileAsArgAttributeName,
	} {
		if _, ok := validation.Attributes[name]; !ok {
			t.Fatalf("expected validation block to contain attribute %q", name)
		}
	}
}

func TestFileInfoSchemaUsesSharedContract(t *testing.T) {
	t.Parallel()

	schema := (&fileInfoDataSource{}).DataSchema()
	if _, ok := schema.Attributes["is_dir"]; ok {
		t.Fatal("expected file_info schema to omit is_dir")
	}
	for name := range linuxfilescontract.FileInfoDataSourceSchema().Attributes {
		if _, ok := schema.Attributes[name]; !ok {
			t.Fatalf("expected file_info schema to contain attribute %q", name)
		}
	}
}

func TestFileStateSensitiveDefaultsTrue(t *testing.T) {
	t.Parallel()

	if !fileStateSensitive(map[string]interface{}{}) {
		t.Fatal("expected sensitive mode to default to true")
	}
}

func TestPlannedContentStateValueSensitive(t *testing.T) {
	t.Parallel()

	plan := map[string]interface{}{"content": "hello", "sensitive": true}
	if got := plannedContentStateValue(plan, "blake3:abc123", false); got != "blake3:abc123" {
		t.Fatalf("expected hashed state marker, got %q", got)
	}
}

func TestPlannedContentStateValueNonSensitive(t *testing.T) {
	t.Parallel()

	plan := map[string]interface{}{"content": "hello", "sensitive": false}
	if got := plannedContentStateValue(plan, "abc123", false); got != "hello" {
		t.Fatalf("expected raw content in state, got %q", got)
	}
}

func TestSerializeKeyValueLines(t *testing.T) {
	t.Parallel()

	entries := []keyValueLine{{Comment: true, Raw: "# header"}, {Key: "net.ipv4.ip_forward", Value: "1"}}
	if got := serializeKeyValueLines(entries, "="); got != "# header\nnet.ipv4.ip_forward=1" {
		t.Fatalf("unexpected serialization: %q", got)
	}
}

func TestNetworkStackUpdateWritesDedicatedConfigAndAppliesManagedSysctls(t *testing.T) {
	origFileStat := pluginsdk.FileStat_
	origDirEnsure := pluginsdk.DirEnsure
	origFileWrite := pluginsdk.FileWrite
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.FileStat_ = origFileStat
		pluginsdk.DirEnsure = origDirEnsure
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.CmdExec = origCmdExec
	})

	var ensuredPath string
	var ensuredMode uint32
	var wrotePath string
	var wroteData []byte
	commands := make([]string, 0, 8)

	pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
		if path != "/etc/sysctl.d" {
			t.Fatalf("unexpected stat path %q", path)
		}
		return nil, assertErr("stat /etc/sysctl.d: no such file or directory")
	}
	pluginsdk.DirEnsure = func(path string, mode uint32) error {
		ensuredPath = path
		ensuredMode = mode
		return nil
	}
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		wrotePath = path
		wroteData = append([]byte(nil), data...)
		return nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		commands = append(commands, cmd+" "+strings.Join(args, " "))
		return &pluginsdk.CmdResult{ExitCode: 0}, nil
	}

	state, err := (&networkStackResource{}).Update(nil, pluginsdk.StateData{
		"ipv4_forwarding":       true,
		"ipv6_forwarding":       false,
		"bridge_netfilter_ipv4": true,
		"bridge_netfilter_ipv6": true,
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if wrotePath != networkStackConfigPath {
		t.Fatalf("unexpected write path %q", wrotePath)
	}
	expectedContent := "# managed by tf-nix network_stack\nnet.ipv4.ip_forward=1\nnet.ipv6.conf.all.forwarding=0\nnet.ipv6.conf.default.forwarding=0\nnet.bridge.bridge-nf-call-iptables=1\nnet.bridge.bridge-nf-call-ip6tables=1\n"
	if string(wroteData) != expectedContent {
		t.Fatalf("unexpected network stack config:\n%s", string(wroteData))
	}
	if ensuredPath != "/etc/sysctl.d" || ensuredMode != 0o755 {
		t.Fatalf("unexpected ensured directory: path=%q mode=%#o", ensuredPath, ensuredMode)
	}
	expectedCommands := []string{
		"sysctl -w net.ipv4.ip_forward=1",
		"sysctl -w net.ipv6.conf.all.forwarding=0",
		"sysctl -w net.ipv6.conf.default.forwarding=0",
		"sysctl -w net.bridge.bridge-nf-call-iptables=1",
		"sysctl -w net.bridge.bridge-nf-call-ip6tables=1",
	}
	if strings.Join(commands, "\n") != strings.Join(expectedCommands, "\n") {
		t.Fatalf("unexpected commands: %#v", commands)
	}
	if !state.GetBool("ipv4_forwarding") || state.GetBool("ipv6_forwarding") {
		t.Fatalf("unexpected state booleans: %#v", state)
	}
	if got := state.GetMap("sysctls"); got["net.ipv4.ip_forward"] != "1" || got["net.ipv6.conf.all.forwarding"] != "0" {
		t.Fatalf("unexpected sysctls map: %#v", got)
	}
}

func TestNetworkStackReadParsesManagedFile(t *testing.T) {
	origFileRead := pluginsdk.FileRead
	t.Cleanup(func() {
		pluginsdk.FileRead = origFileRead
	})

	pluginsdk.FileRead = func(path string) ([]byte, error) {
		if path != networkStackConfigPath {
			t.Fatalf("unexpected read path %q", path)
		}
		return []byte("# managed by tf-nix network_stack\nnet.ipv4.ip_forward=1\nnet.ipv6.conf.all.forwarding=1\nnet.ipv6.conf.default.forwarding=1\nnet.bridge.bridge-nf-call-iptables=0\nnet.bridge.bridge-nf-call-ip6tables=1\n"), nil
	}

	state, err := (&networkStackResource{}).Read(nil)
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if state.GetString("id") != networkStackID() {
		t.Fatalf("unexpected network stack ID: %#v", state)
	}
	if !state.GetBool("ipv4_forwarding") || !state.GetBool("ipv6_forwarding") {
		t.Fatalf("expected forwarding booleans to be true: %#v", state)
	}
	if state.GetBool("bridge_netfilter_ipv4") {
		t.Fatalf("expected IPv4 bridge netfilter to be false: %#v", state)
	}
	if !state.GetBool("bridge_netfilter_ipv6") {
		t.Fatalf("expected IPv6 bridge netfilter to be true: %#v", state)
	}
}

func TestNormalizeHostAliases(t *testing.T) {
	t.Parallel()

	got := normalizeHostAliases("example.local", []string{"alias1", "example.local", "alias1", "alias2", ""})
	if len(got) != 2 || got[0] != "alias1" || got[1] != "alias2" {
		t.Fatalf("unexpected aliases: %#v", got)
	}
}

func TestHostsCommentRoundTripHelpers(t *testing.T) {
	t.Parallel()

	serialized := serializeHostsComment("managed by tf-nix")
	if serialized != "# managed by tf-nix" {
		t.Fatalf("unexpected serialized comment: %q", serialized)
	}
	if got := hostsCommentState(serialized); got != "managed by tf-nix" {
		t.Fatalf("unexpected state comment: %q", got)
	}
}

func TestIsNotExistError(t *testing.T) {
	t.Parallel()

	if !isNotExistError(assertErr("stat /tmp/missing: no such file or directory")) {
		t.Fatal("expected missing-file error to be classified as not-exist")
	}
	if isNotExistError(assertErr("permission denied")) {
		t.Fatal("did not expect permission error to be classified as not-exist")
	}
}

func TestFileCreateExistingRequiresImport(t *testing.T) {
	origFileStat := pluginsdk.FileStat_
	origFileWrite := pluginsdk.FileWrite
	origFileChown := pluginsdk.FileChown
	t.Cleanup(func() {
		pluginsdk.FileStat_ = origFileStat
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.FileChown = origFileChown
	})

	pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
		return &pluginsdk.FileStat{Path: path, Digest: "blake3:abc", Owner: "root", Group: "root", Mode: 0o644}, nil
	}
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		t.Fatalf("FileWrite should not be called for pre-existing file %s", path)
		return nil
	}
	pluginsdk.FileChown = func(path string, owner, group string) error {
		t.Fatalf("FileChown should not be called for pre-existing file %s", path)
		return nil
	}

	_, err := (&fileResource{}).Create(pluginsdk.StateData{
		"path":    "/etc/app.conf",
		"content": "demo",
	})
	if err == nil || !strings.Contains(err.Error(), "import it before managing with terraform") {
		t.Fatalf("expected import-required error, got %v", err)
	}
}

func TestSymlinkCreateExistingRequiresImport(t *testing.T) {
	origFileReadlink := pluginsdk.FileReadlink
	origFileExists := pluginsdk.FileExists
	t.Cleanup(func() {
		pluginsdk.FileReadlink = origFileReadlink
		pluginsdk.FileExists = origFileExists
	})

	pluginsdk.FileReadlink = func(path string) (string, error) {
		return "/target", nil
	}
	pluginsdk.FileExists = func(path string) (bool, error) {
		return true, nil
	}

	_, err := (&symlinkResource{}).Create(pluginsdk.StateData{
		"path":   "/etc/nginx/sites-enabled/app",
		"target": "/etc/nginx/sites-available/app",
	})
	if err == nil || !strings.Contains(err.Error(), "import it before managing with terraform") {
		t.Fatalf("expected import-required error, got %v", err)
	}
}

func TestSymlinkReadReturnsTarget(t *testing.T) {
	origFileReadlink := pluginsdk.FileReadlink
	origFileExists := pluginsdk.FileExists
	t.Cleanup(func() {
		pluginsdk.FileReadlink = origFileReadlink
		pluginsdk.FileExists = origFileExists
	})

	pluginsdk.FileReadlink = func(path string) (string, error) {
		return "/etc/nginx/sites-available/app", nil
	}
	pluginsdk.FileExists = func(path string) (bool, error) {
		return true, nil
	}

	state, err := (&symlinkResource{}).Read(pluginsdk.StateData{"path": "/etc/nginx/sites-enabled/app"})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if state.GetString("target") != "/etc/nginx/sites-available/app" {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestSymlinkUpdateCreatesLink(t *testing.T) {
	origFileStat := pluginsdk.FileStat_
	origDirEnsure := pluginsdk.DirEnsure
	origFileSymlink := pluginsdk.FileSymlink
	t.Cleanup(func() {
		pluginsdk.FileStat_ = origFileStat
		pluginsdk.DirEnsure = origDirEnsure
		pluginsdk.FileSymlink = origFileSymlink
	})

	var ensuredPath string
	var ensuredMode uint32
	var linkedTarget, linkedPath string
	pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
		if path != "/etc/nginx/sites-enabled" {
			t.Fatalf("unexpected stat path %q", path)
		}
		return nil, assertErr("stat /etc/nginx/sites-enabled: no such file or directory")
	}
	pluginsdk.DirEnsure = func(path string, mode uint32) error {
		ensuredPath = path
		ensuredMode = mode
		return nil
	}
	pluginsdk.FileSymlink = func(target, path string) error {
		linkedTarget = target
		linkedPath = path
		return nil
	}

	state, err := (&symlinkResource{}).Update(nil, pluginsdk.StateData{
		"path":   "/etc/nginx/sites-enabled/app",
		"target": "/etc/nginx/sites-available/app",
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if ensuredPath != "/etc/nginx/sites-enabled" || ensuredMode != 0o755 {
		t.Fatalf("unexpected ensured directory: path=%q mode=%#o", ensuredPath, ensuredMode)
	}
	if linkedTarget != "/etc/nginx/sites-available/app" || linkedPath != "/etc/nginx/sites-enabled/app" {
		t.Fatalf("unexpected symlink call: target=%q path=%q", linkedTarget, linkedPath)
	}
	if state.GetString("target") != "/etc/nginx/sites-available/app" {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestFileUpdateWritesExistingManagedFile(t *testing.T) {
	origFileStat := pluginsdk.FileStat_
	origDirEnsure := pluginsdk.DirEnsure
	origFileWrite := pluginsdk.FileWrite
	origFileChown := pluginsdk.FileChown
	t.Cleanup(func() {
		pluginsdk.FileStat_ = origFileStat
		pluginsdk.DirEnsure = origDirEnsure
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.FileChown = origFileChown
	})

	var fileStatCalls int
	var ensuredPath string
	var ensuredMode uint32
	var wrotePath string
	var wroteMode uint32
	var wroteData []byte
	var chownPath, chownOwner, chownGroup string

	pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
		switch path {
		case "/etc/app":
			return nil, assertErr("stat /etc/app: no such file or directory")
		case "/etc/app/app.conf":
			fileStatCalls++
			if fileStatCalls == 1 {
				return nil, assertErr("stat /etc/app/app.conf: no such file or directory")
			}
			return &pluginsdk.FileStat{Path: path, Digest: "blake3:new", Owner: "app", Group: "app", Mode: 0o640}, nil
		default:
			t.Fatalf("unexpected stat path %q", path)
			return nil, nil
		}
	}
	pluginsdk.DirEnsure = func(path string, mode uint32) error {
		ensuredPath = path
		ensuredMode = mode
		return nil
	}
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		wrotePath = path
		wroteMode = mode
		wroteData = append([]byte(nil), data...)
		return nil
	}
	pluginsdk.FileChown = func(path string, owner, group string) error {
		chownPath = path
		chownOwner = owner
		chownGroup = group
		return nil
	}

	state, err := (&fileResource{}).Update(nil, pluginsdk.StateData{
		"path":    "/etc/app/app.conf",
		"content": "updated",
		"owner":   "app",
		"group":   "app",
		"mode":    "0640",
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if ensuredPath != "/etc/app" || ensuredMode != 0o755 {
		t.Fatalf("unexpected ensured directory: path=%q mode=%#o", ensuredPath, ensuredMode)
	}
	if wrotePath != "/etc/app/app.conf" || string(wroteData) != "updated" || wroteMode != 0o640 {
		t.Fatalf("unexpected file write: path=%q mode=%#o data=%q", wrotePath, wroteMode, string(wroteData))
	}
	if chownPath != "/etc/app/app.conf" || chownOwner != "app" || chownGroup != "app" {
		t.Fatalf("unexpected chown: path=%q owner=%q group=%q", chownPath, chownOwner, chownGroup)
	}
	if state.GetString("digest") != "blake3:new" || state.GetString("mode") != "0640" {
		t.Fatalf("unexpected state after update: %#v", state)
	}
}

func TestFileUpdateWithValidationStagesCandidateBeforeMove(t *testing.T) {
	origFileStat := pluginsdk.FileStat_
	origDirEnsure := pluginsdk.DirEnsure
	origFileWrite := pluginsdk.FileWrite
	origFileChown := pluginsdk.FileChown
	origFileRename := pluginsdk.FileRename
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.FileStat_ = origFileStat
		pluginsdk.DirEnsure = origDirEnsure
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.FileChown = origFileChown
		pluginsdk.FileRename = origFileRename
		pluginsdk.CmdExec = origCmdExec
	})

	const finalPath = "/etc/app/app.conf"
	var finalStatCalls int
	var ensuredPath string
	var ensuredMode uint32
	var wrotePath string
	var wroteData []byte
	var wroteMode uint32
	var chownPath, chownOwner, chownGroup string
	var renameFrom, renameTo string
	commands := make([]string, 0, 2)

	pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
		switch path {
		case "/etc/app":
			return nil, assertErr("stat /etc/app: no such file or directory")
		case finalPath:
			finalStatCalls++
			if finalStatCalls == 1 {
				return nil, assertErr("stat " + finalPath + ": no such file or directory")
			}
			return &pluginsdk.FileStat{Path: path, Digest: "blake3:new", Owner: "app", Group: "app", Mode: 0o640}, nil
		default:
			return nil, assertErr("stat " + path + ": no such file or directory")
		}
	}
	pluginsdk.DirEnsure = func(path string, mode uint32) error {
		ensuredPath = path
		ensuredMode = mode
		return nil
	}
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		wrotePath = path
		wroteData = append([]byte(nil), data...)
		wroteMode = mode
		return nil
	}
	pluginsdk.FileChown = func(path string, owner, group string) error {
		chownPath = path
		chownOwner = owner
		chownGroup = group
		return nil
	}
	pluginsdk.FileRename = func(from, to string) error {
		renameFrom = from
		renameTo = to
		return nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		commands = append(commands, cmd+" "+strings.Join(args, " "))
		switch cmd {
		case "validator":
			if len(args) != 2 || args[0] != "--check" || !strings.Contains(args[1], ".app.conf.tf-nix-candidate-") {
				t.Fatalf("unexpected validator args: %#v", args)
			}
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		default:
			t.Fatalf("unexpected command %q %#v", cmd, args)
			return nil, nil
		}
	}

	state, err := (&fileResource{}).Update(nil, pluginsdk.StateData{
		"path":    finalPath,
		"content": "updated",
		"owner":   "app",
		"group":   "app",
		"mode":    "0640",
		"validation": map[string]interface{}{
			"argv": []interface{}{"validator", "--check"},
		},
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if wrotePath == finalPath || !strings.Contains(wrotePath, ".app.conf.tf-nix-candidate-") {
		t.Fatalf("expected staged candidate write path, got %q", wrotePath)
	}
	if string(wroteData) != "updated" || wroteMode != 0o640 {
		t.Fatalf("unexpected staged write: mode=%#o data=%q", wroteMode, string(wroteData))
	}
	if chownPath != wrotePath || chownOwner != "app" || chownGroup != "app" {
		t.Fatalf("unexpected staged chown: path=%q owner=%q group=%q", chownPath, chownOwner, chownGroup)
	}
	if ensuredPath != "/etc/app" || ensuredMode != 0o755 {
		t.Fatalf("unexpected ensured directory: path=%q mode=%#o", ensuredPath, ensuredMode)
	}
	if renameTo != finalPath || !strings.Contains(renameFrom, ".app.conf.tf-nix-candidate-") {
		t.Fatalf("unexpected file rename: from=%q to=%q", renameFrom, renameTo)
	}
	if len(commands) != 1 || !strings.HasPrefix(commands[0], "validator --check ") {
		t.Fatalf("unexpected commands: %#v", commands)
	}
	validation, ok := state["validation"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected validation state, got %#v", state["validation"])
	}
	if validation["in_place"] != false {
		t.Fatalf("expected in_place=false in state, got %#v", validation["in_place"])
	}
	if validation["file_as_arg"] != true {
		t.Fatalf("expected file_as_arg=true in state, got %#v", validation["file_as_arg"])
	}
	if state.GetString("digest") != "blake3:new" {
		t.Fatalf("unexpected state after staged validation: %#v", state)
	}
}

func TestFileUpdateWithValidationCanSkipFileArgument(t *testing.T) {
	origFileStat := pluginsdk.FileStat_
	origDirEnsure := pluginsdk.DirEnsure
	origFileWrite := pluginsdk.FileWrite
	origFileChown := pluginsdk.FileChown
	origFileRename := pluginsdk.FileRename
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.FileStat_ = origFileStat
		pluginsdk.DirEnsure = origDirEnsure
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.FileChown = origFileChown
		pluginsdk.FileRename = origFileRename
		pluginsdk.CmdExec = origCmdExec
	})

	const finalPath = "/etc/nginx/sites-available/app.conf"
	var finalStatCalls int
	var ensuredPath string
	var ensuredMode uint32
	var renameFrom, renameTo string
	commands := make([]string, 0, 2)

	pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
		switch path {
		case "/etc/nginx/sites-available":
			return nil, assertErr("stat /etc/nginx/sites-available: no such file or directory")
		case finalPath:
			finalStatCalls++
			if finalStatCalls == 1 {
				return nil, assertErr("stat " + finalPath + ": no such file or directory")
			}
			return &pluginsdk.FileStat{Path: path, Digest: "blake3:new", Owner: "root", Group: "root", Mode: 0o644}, nil
		default:
			return nil, assertErr("stat " + path + ": no such file or directory")
		}
	}
	pluginsdk.DirEnsure = func(path string, mode uint32) error {
		ensuredPath = path
		ensuredMode = mode
		return nil
	}
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		return nil
	}
	pluginsdk.FileChown = func(path string, owner, group string) error {
		return nil
	}
	pluginsdk.FileRename = func(from, to string) error {
		renameFrom = from
		renameTo = to
		return nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		commands = append(commands, cmd+" "+strings.Join(args, " "))
		switch cmd {
		case "nginx":
			if len(args) != 1 || args[0] != "-t" {
				t.Fatalf("unexpected nginx args: %#v", args)
			}
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		default:
			t.Fatalf("unexpected command %q %#v", cmd, args)
			return nil, nil
		}
	}

	state, err := (&fileResource{}).Update(nil, pluginsdk.StateData{
		"path":    finalPath,
		"content": "server {}",
		"validation": map[string]interface{}{
			"argv":        []interface{}{"nginx", "-t"},
			"file_as_arg": false,
		},
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if ensuredPath != "/etc/nginx/sites-available" || ensuredMode != 0o755 {
		t.Fatalf("unexpected ensured directory: path=%q mode=%#o", ensuredPath, ensuredMode)
	}
	if renameTo != finalPath || renameFrom == "" {
		t.Fatalf("unexpected file rename: from=%q to=%q", renameFrom, renameTo)
	}
	if len(commands) != 1 || commands[0] != "nginx -t" {
		t.Fatalf("unexpected commands: %#v", commands)
	}
	validation, ok := state["validation"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected validation state, got %#v", state["validation"])
	}
	if validation["file_as_arg"] != false {
		t.Fatalf("expected file_as_arg=false in state, got %#v", validation["file_as_arg"])
	}
}

func TestFileReadIncludesNonSensitiveContentAndValidationState(t *testing.T) {
	origFileStat := pluginsdk.FileStat_
	origFileRead := pluginsdk.FileRead
	t.Cleanup(func() {
		pluginsdk.FileStat_ = origFileStat
		pluginsdk.FileRead = origFileRead
	})

	pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
		if path != "/etc/app/app.conf" {
			t.Fatalf("unexpected stat path %q", path)
		}
		return &pluginsdk.FileStat{Path: path, Owner: "app", Group: "app", Mode: 0o640, Digest: "blake3:abc123"}, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		if path != "/etc/app/app.conf" {
			t.Fatalf("unexpected read path %q", path)
		}
		return []byte("current content"), nil
	}

	state, err := (&fileResource{}).Read(pluginsdk.StateData{
		"path":      "/etc/app/app.conf",
		"content":   "stale",
		"sensitive": false,
		"run_as":    "root",
		"validation": map[string]interface{}{
			"argv": []interface{}{"validator", "--check"},
		},
	})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if state.GetString("content") != "current content" || state.GetString("mode") != "0640" || state.GetString("run_as") != "root" {
		t.Fatalf("unexpected file read state: %#v", state)
	}
	if _, ok := state["validation"].(map[string]interface{}); !ok {
		t.Fatalf("expected validation state map, got %#v", state["validation"])
	}
}

func TestFileUpdateWithValidationInPlaceRollsBackOnFailure(t *testing.T) {
	origFileStat := pluginsdk.FileStat_
	origDirEnsure := pluginsdk.DirEnsure
	origFileWrite := pluginsdk.FileWrite
	origFileChown := pluginsdk.FileChown
	origFileRename := pluginsdk.FileRename
	origFileDelete := pluginsdk.FileDelete
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.FileStat_ = origFileStat
		pluginsdk.DirEnsure = origDirEnsure
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.FileChown = origFileChown
		pluginsdk.FileRename = origFileRename
		pluginsdk.FileDelete = origFileDelete
		pluginsdk.CmdExec = origCmdExec
	})

	const finalPath = "/etc/app/app.conf"
	var wrotePath string
	var ensuredPath string
	var ensuredMode uint32
	renames := make([][2]string, 0, 3)
	deletePaths := make([]string, 0, 1)
	commands := make([]string, 0, 2)

	pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
		switch path {
		case "/etc/app":
			return nil, assertErr("stat /etc/app: no such file or directory")
		case finalPath:
			return &pluginsdk.FileStat{Path: path, Digest: "blake3:old", Owner: "app", Group: "app", Mode: 0o640}, nil
		default:
			return nil, assertErr("stat " + path + ": no such file or directory")
		}
	}
	pluginsdk.DirEnsure = func(path string, mode uint32) error {
		ensuredPath = path
		ensuredMode = mode
		return nil
	}
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		wrotePath = path
		return nil
	}
	pluginsdk.FileChown = func(path string, owner, group string) error {
		return nil
	}
	pluginsdk.FileRename = func(from, to string) error {
		renames = append(renames, [2]string{from, to})
		return nil
	}
	pluginsdk.FileDelete = func(path string) error {
		deletePaths = append(deletePaths, path)
		return nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		commands = append(commands, cmd+" "+strings.Join(args, " "))
		switch cmd {
		case "validator":
			if len(args) != 2 || args[0] != "--check" || args[1] != finalPath {
				t.Fatalf("unexpected validator args: %#v", args)
			}
			return &pluginsdk.CmdResult{ExitCode: 1, Stderr: "bad config"}, nil
		default:
			t.Fatalf("unexpected command %q %#v", cmd, args)
			return nil, nil
		}
	}

	_, err := (&fileResource{}).Update(nil, pluginsdk.StateData{
		"path":    finalPath,
		"content": "updated",
		"owner":   "app",
		"group":   "app",
		"mode":    "0640",
		"validation": map[string]interface{}{
			"argv":     []interface{}{"validator", "--check"},
			"in_place": true,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "bad config") {
		t.Fatalf("expected validation failure, got %v", err)
	}
	if wrotePath == finalPath || !strings.Contains(wrotePath, ".app.conf.tf-nix-candidate-") {
		t.Fatalf("expected staged candidate write path, got %q", wrotePath)
	}
	if ensuredPath != "/etc/app" || ensuredMode != 0o755 {
		t.Fatalf("unexpected ensured directory: path=%q mode=%#o", ensuredPath, ensuredMode)
	}
	if len(renames) != 3 {
		t.Fatalf("unexpected rename calls: %#v", renames)
	}
	if renames[0][0] != finalPath || !strings.Contains(renames[0][1], ".app.conf.tf-nix-backup-") {
		t.Fatalf("unexpected backup rename: %#v", renames[0])
	}
	if renames[1][0] != wrotePath || renames[1][1] != finalPath {
		t.Fatalf("unexpected candidate rename: %#v", renames[1])
	}
	if len(deletePaths) != 1 || deletePaths[0] != finalPath {
		t.Fatalf("unexpected rollback delete paths: %#v", deletePaths)
	}
	if renames[2][1] != finalPath || !strings.Contains(renames[2][0], ".app.conf.tf-nix-backup-") {
		t.Fatalf("unexpected rollback restore rename: %#v", renames[2])
	}
	if len(commands) != 1 {
		t.Fatalf("unexpected command count: %#v", commands)
	}
	if commands[0] != "validator --check /etc/app/app.conf" {
		t.Fatalf("unexpected validator command: %#v", commands[0])
	}
}

func TestKernelModulesUpdateWritesConfigAndLoadsModules(t *testing.T) {
	origFileStat := pluginsdk.FileStat_
	origDirEnsure := pluginsdk.DirEnsure
	origFileWrite := pluginsdk.FileWrite
	origFileChown := pluginsdk.FileChown
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.FileStat_ = origFileStat
		pluginsdk.DirEnsure = origDirEnsure
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.FileChown = origFileChown
		pluginsdk.CmdExec = origCmdExec
	})

	var ensuredPath string
	var ensuredMode uint32
	var wrotePath string
	var wroteData []byte
	var wroteMode uint32
	var chownPath, chownOwner, chownGroup string
	commands := make([]string, 0, 3)

	pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
		if path != "/etc/modules-load.d" {
			t.Fatalf("unexpected stat path %q", path)
		}
		return nil, assertErr("stat /etc/modules-load.d: no such file or directory")
	}
	pluginsdk.DirEnsure = func(path string, mode uint32) error {
		ensuredPath = path
		ensuredMode = mode
		return nil
	}
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		wrotePath = path
		wroteData = append([]byte(nil), data...)
		wroteMode = mode
		return nil
	}
	pluginsdk.FileChown = func(path string, owner, group string) error {
		chownPath = path
		chownOwner = owner
		chownGroup = group
		return nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		commands = append(commands, cmd+" "+strings.Join(args, " "))
		return &pluginsdk.CmdResult{ExitCode: 0}, nil
	}

	state, err := (&kernelModulesResource{}).Update(nil, pluginsdk.StateData{
		"path":    "/etc/modules-load.d/90-tf-nix-kubernetes.conf",
		"modules": []string{"overlay", "br_netfilter", "overlay"},
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if wrotePath != "/etc/modules-load.d/90-tf-nix-kubernetes.conf" || string(wroteData) != "overlay\nbr_netfilter\n" || wroteMode != 0o644 {
		t.Fatalf("unexpected file write: path=%q mode=%#o data=%q", wrotePath, wroteMode, string(wroteData))
	}
	if chownPath != wrotePath || chownOwner != "root" || chownGroup != "root" {
		t.Fatalf("unexpected chown: path=%q owner=%q group=%q", chownPath, chownOwner, chownGroup)
	}
	if ensuredPath != "/etc/modules-load.d" || ensuredMode != 0o755 {
		t.Fatalf("unexpected ensured directory: path=%q mode=%#o", ensuredPath, ensuredMode)
	}
	if len(commands) != 2 || commands[0] != "modprobe overlay" || commands[1] != "modprobe br_netfilter" {
		t.Fatalf("unexpected commands: %#v", commands)
	}
	if got := state.GetStringList("modules"); len(got) != 2 || got[0] != "overlay" || got[1] != "br_netfilter" {
		t.Fatalf("unexpected state modules: %#v", got)
	}
}

func TestKernelModulesReadParsesManagedFile(t *testing.T) {
	origFileStat := pluginsdk.FileStat_
	origFileRead := pluginsdk.FileRead
	t.Cleanup(func() {
		pluginsdk.FileStat_ = origFileStat
		pluginsdk.FileRead = origFileRead
	})

	pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
		return &pluginsdk.FileStat{Path: path, Mode: 0o644, Owner: "root", Group: "root"}, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		return []byte("overlay\n# comment\nbr_netfilter\n"), nil
	}

	state, err := (&kernelModulesResource{}).Read(pluginsdk.StateData{"path": "/etc/modules-load.d/90-tf-nix-kubernetes.conf"})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if got := state.GetStringList("modules"); len(got) != 2 || got[0] != "overlay" || got[1] != "br_netfilter" {
		t.Fatalf("unexpected state modules: %#v", got)
	}
}

func TestSwapUpdateDisablesRuntimeAndCommentsManagedFstabEntries(t *testing.T) {
	origFileLock := pluginsdk.FileLock
	origFileUnlock := pluginsdk.FileUnlock
	origFileRead := pluginsdk.FileRead
	origFileWrite := pluginsdk.FileWrite
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.FileLock = origFileLock
		pluginsdk.FileUnlock = origFileUnlock
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.CmdExec = origCmdExec
	})

	pluginsdk.FileLock = func(path string) (uint32, error) {
		if path != fstabConfigPath {
			t.Fatalf("unexpected lock path %q", path)
		}
		return 1, nil
	}
	pluginsdk.FileUnlock = func(handle uint32) error {
		if handle != 1 {
			t.Fatalf("unexpected unlock handle %d", handle)
		}
		return nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		switch path {
		case swapInfoPath:
			return []byte("Filename\tType\tSize\tUsed\tPriority\n/swapfile file 1024 0 -2\n"), nil
		case fstabConfigPath:
			return []byte("/swapfile none swap sw 0 0\nUUID=root / ext4 defaults 0 1\n"), nil
		default:
			t.Fatalf("unexpected read path %q", path)
			return nil, nil
		}
	}
	var wrote string
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		if path != fstabConfigPath {
			t.Fatalf("unexpected write path %q", path)
		}
		wrote = string(data)
		return nil
	}
	commands := make([]string, 0, 1)
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		commands = append(commands, cmd+" "+strings.Join(args, " "))
		return &pluginsdk.CmdResult{ExitCode: 0}, nil
	}

	state, err := (&swapResource{}).Update(nil, pluginsdk.StateData{"enabled": false})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if wrote != "# tf-nix swap disabled: /swapfile\tnone\tswap\tsw\t0\t0\nUUID=root\t/\text4\tdefaults\t0\t1\n" {
		t.Fatalf("unexpected fstab content: %q", wrote)
	}
	if len(commands) != 1 || commands[0] != "swapoff -a" {
		t.Fatalf("unexpected commands: %#v", commands)
	}
	if !state.GetBool("enabled") && state.GetString("id") != "system" {
		t.Fatalf("unexpected state: %#v", state)
	}
	if state.GetBool("enabled") {
		t.Fatalf("expected disabled swap state, got %#v", state)
	}
}

func TestSwapUpdateRestoresManagedFstabEntriesAndRunsSwapon(t *testing.T) {
	origFileLock := pluginsdk.FileLock
	origFileUnlock := pluginsdk.FileUnlock
	origFileRead := pluginsdk.FileRead
	origFileWrite := pluginsdk.FileWrite
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.FileLock = origFileLock
		pluginsdk.FileUnlock = origFileUnlock
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.CmdExec = origCmdExec
	})

	pluginsdk.FileLock = func(path string) (uint32, error) { return 1, nil }
	pluginsdk.FileUnlock = func(handle uint32) error { return nil }
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		switch path {
		case swapInfoPath:
			return []byte("Filename\tType\tSize\tUsed\tPriority\n"), nil
		case fstabConfigPath:
			return []byte("# tf-nix swap disabled: /swapfile\tnone\tswap\tsw\t0\t0\nUUID=root / ext4 defaults 0 1\n"), nil
		default:
			t.Fatalf("unexpected read path %q", path)
			return nil, nil
		}
	}
	var wrote string
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		wrote = string(data)
		return nil
	}
	commands := make([]string, 0, 1)
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		commands = append(commands, cmd+" "+strings.Join(args, " "))
		return &pluginsdk.CmdResult{ExitCode: 0}, nil
	}

	state, err := (&swapResource{}).Update(nil, pluginsdk.StateData{"enabled": true})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if wrote != "/swapfile\tnone\tswap\tsw\t0\t0\nUUID=root\t/\text4\tdefaults\t0\t1\n" {
		t.Fatalf("unexpected fstab content: %q", wrote)
	}
	if len(commands) != 1 || commands[0] != "swapon -a" {
		t.Fatalf("unexpected commands: %#v", commands)
	}
	if !state.GetBool("enabled") || state.GetString("id") != "system" {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestSwapDeleteNoOpsWithoutManagedDisabledEntries(t *testing.T) {
	origFileLock := pluginsdk.FileLock
	origFileUnlock := pluginsdk.FileUnlock
	origFileRead := pluginsdk.FileRead
	origFileWrite := pluginsdk.FileWrite
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.FileLock = origFileLock
		pluginsdk.FileUnlock = origFileUnlock
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.CmdExec = origCmdExec
	})

	pluginsdk.FileLock = func(path string) (uint32, error) { return 1, nil }
	pluginsdk.FileUnlock = func(handle uint32) error { return nil }
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		switch path {
		case swapInfoPath:
			return []byte("Filename\tType\tSize\tUsed\tPriority\n"), nil
		case fstabConfigPath:
			return []byte("UUID=root / ext4 defaults 0 1\n"), nil
		default:
			t.Fatalf("unexpected read path %q", path)
			return nil, nil
		}
	}
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		t.Fatalf("FileWrite should not be called, got %q", path)
		return nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		t.Fatalf("CmdExec should not be called, got %s %v", cmd, args)
		return nil, nil
	}

	if err := (&swapResource{}).Delete(pluginsdk.StateData{"enabled": false}); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
}

func TestFileInfoDataSourceIncludesMTimeUnix(t *testing.T) {
	origFileStat := pluginsdk.FileStat_
	t.Cleanup(func() { pluginsdk.FileStat_ = origFileStat })

	pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
		return &pluginsdk.FileStat{
			Path:    path,
			Size:    42,
			Mode:    0o644,
			Owner:   "root",
			Group:   "root",
			Digest:  "blake3:abc123",
			ModTime: 1710000000,
		}, nil
	}

	state, err := (&fileInfoDataSource{}).DataRead(pluginsdk.StateData{"path": "/etc/example"})
	if err != nil {
		t.Fatalf("DataRead returned error: %v", err)
	}
	if got := state.GetInt("mtime_unix"); got != 1710000000 {
		t.Fatalf("expected mtime_unix 1710000000, got %d", got)
	}
}

func TestFileInfoDataSourcePreservesRunAs(t *testing.T) {
	origFileStat := pluginsdk.FileStat_
	t.Cleanup(func() { pluginsdk.FileStat_ = origFileStat })

	pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
		return &pluginsdk.FileStat{
			Path:    path,
			Size:    42,
			Mode:    0o644,
			Owner:   "tf",
			Group:   "tf",
			Digest:  "blake3:abc123",
			ModTime: 1710000000,
		}, nil
	}

	state, err := (&fileInfoDataSource{}).DataRead(pluginsdk.StateData{"path": "/home/tf/tf-nix-smoke-file.txt", "run_as": "tf"})
	if err != nil {
		t.Fatalf("DataRead returned error: %v", err)
	}
	if got := state.GetString("run_as"); got != "tf" {
		t.Fatalf("run_as = %q, want tf", got)
	}

	pluginsdk.FileStat_ = func(path string) (*pluginsdk.FileStat, error) {
		return nil, os.ErrNotExist
	}
	state, err = (&fileInfoDataSource{}).DataRead(pluginsdk.StateData{"path": "/home/tf/missing.txt", "run_as": "tf"})
	if err != nil {
		t.Fatalf("DataRead missing returned error: %v", err)
	}
	if got := state.GetString("run_as"); got != "tf" {
		t.Fatalf("missing run_as = %q, want tf", got)
	}
	if state.GetBool("exists") {
		t.Fatal("expected missing file to report exists=false")
	}
}

func TestHostsEntryCreateExistingRequiresImport(t *testing.T) {
	origFileLock := pluginsdk.FileLock
	origFileUnlock := pluginsdk.FileUnlock
	origFileRead := pluginsdk.FileRead
	t.Cleanup(func() {
		pluginsdk.FileLock = origFileLock
		pluginsdk.FileUnlock = origFileUnlock
		pluginsdk.FileRead = origFileRead
	})

	pluginsdk.FileLock = func(path string) (uint32, error) { return 1, nil }
	pluginsdk.FileUnlock = func(handle uint32) error { return nil }
	pluginsdk.FileRead = func(path string) ([]byte, error) { return []byte("10.0.0.10 app.internal\n"), nil }

	_, err := (&hostsEntryResource{}).Create(pluginsdk.StateData{
		"ip":       "10.0.0.10",
		"hostname": "app.internal",
	})
	if err == nil || !strings.Contains(err.Error(), "import it before managing with terraform") {
		t.Fatalf("expected import-required error, got %v", err)
	}
}

func TestSysctlEntryCreateExistingRequiresImport(t *testing.T) {
	origFileRead := pluginsdk.FileRead
	t.Cleanup(func() { pluginsdk.FileRead = origFileRead })

	pluginsdk.FileRead = func(path string) ([]byte, error) {
		return []byte("net.ipv4.ip_forward=1\n"), nil
	}

	_, err := (&sysctlEntryResource{}).Create(pluginsdk.StateData{
		"key":   "net.ipv4.ip_forward",
		"value": "0",
	})
	if err == nil || !strings.Contains(err.Error(), "import it before managing with terraform") {
		t.Fatalf("expected import-required error, got %v", err)
	}
}

func TestSysctlEntryUpdateReconcilesExistingManagedEntry(t *testing.T) {
	origFileLock := pluginsdk.FileLock
	origFileUnlock := pluginsdk.FileUnlock
	origFileRead := pluginsdk.FileRead
	origFileWrite := pluginsdk.FileWrite
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.FileLock = origFileLock
		pluginsdk.FileUnlock = origFileUnlock
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.CmdExec = origCmdExec
	})

	content := "net.ipv4.ip_forward=1\n"
	pluginsdk.FileLock = func(path string) (uint32, error) { return 1, nil }
	pluginsdk.FileUnlock = func(handle uint32) error { return nil }
	pluginsdk.FileRead = func(path string) ([]byte, error) { return []byte(content), nil }
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		content = string(data)
		return nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		if cmd == "sysctl" && len(args) == 2 && args[0] == "-w" && args[1] == "net.ipv4.ip_forward=0" {
			return &pluginsdk.CmdResult{ExitCode: 0}, nil
		}
		t.Fatalf("unexpected command: %s %#v", cmd, args)
		return nil, nil
	}

	state, err := (&sysctlEntryResource{}).Update(nil, pluginsdk.StateData{
		"key":   "net.ipv4.ip_forward",
		"value": "0",
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if content != "net.ipv4.ip_forward=0\n" {
		t.Fatalf("unexpected sysctl config contents: %q", content)
	}
	if state.GetString("key") != "net.ipv4.ip_forward" || state.GetString("value") != "0" {
		t.Fatalf("unexpected state after update: %#v", state)
	}
}

func TestFstabEntryCreateExistingRequiresImport(t *testing.T) {
	origFileRead := pluginsdk.FileRead
	t.Cleanup(func() { pluginsdk.FileRead = origFileRead })

	pluginsdk.FileRead = func(path string) ([]byte, error) {
		return []byte("/dev/sda1 / ext4 defaults 0 1\n"), nil
	}

	_, err := (&fstabEntryResource{}).Create(pluginsdk.StateData{
		"device": "/dev/sda1",
		"mount":  "/",
		"fstype": "ext4",
	})
	if err == nil || !strings.Contains(err.Error(), "import it before managing with terraform") {
		t.Fatalf("expected import-required error, got %v", err)
	}
}

func TestFstabEntryUpdateReconcilesExistingManagedEntry(t *testing.T) {
	origFileLock := pluginsdk.FileLock
	origFileUnlock := pluginsdk.FileUnlock
	origFileRead := pluginsdk.FileRead
	origFileWrite := pluginsdk.FileWrite
	t.Cleanup(func() {
		pluginsdk.FileLock = origFileLock
		pluginsdk.FileUnlock = origFileUnlock
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileWrite = origFileWrite
	})

	content := "/dev/sda1\t/\text4\tdefaults\t0\t1\n"
	pluginsdk.FileLock = func(path string) (uint32, error) { return 1, nil }
	pluginsdk.FileUnlock = func(handle uint32) error { return nil }
	pluginsdk.FileRead = func(path string) ([]byte, error) { return []byte(content), nil }
	pluginsdk.FileWrite = func(path string, data []byte, mode uint32) error {
		content = string(data)
		return nil
	}

	state, err := (&fstabEntryResource{}).Update(nil, pluginsdk.StateData{
		"device":  "/dev/sda1",
		"mount":   "/",
		"fstype":  "ext4",
		"options": []interface{}{"defaults", "noatime"},
		"dump":    0,
		"passno":  1,
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if content != "/dev/sda1\t/\text4\tdefaults,noatime\t0\t1\n" {
		t.Fatalf("unexpected fstab contents: %q", content)
	}
	if state.GetString("mount") != "/" || len(state.GetStringList("options")) != 2 {
		t.Fatalf("unexpected state after update: %#v", state)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func assertErr(msg string) error { return errString(msg) }
