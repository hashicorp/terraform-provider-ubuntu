package runtime

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	stdruntime "runtime"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-provider-ubuntu/executor/capabilities"
	"github.com/hashicorp/terraform-provider-ubuntu/executor/runtime/plugincodec"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/assetmanifest"
	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

func TestWASMAptRepositoryCreatePreservesEmptyLists(t *testing.T) {
	if _, err := exec.LookPath("tinygo"); err != nil {
		t.Skipf("tinygo not available: %v", err)
	}
	if _, err := exec.LookPath("wasm-opt"); err != nil {
		t.Skipf("wasm-opt not available: %v", err)
	}

	_, file, _, ok := stdruntime.Caller(0)
	if !ok {
		t.Fatal("resolve caller")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	wasmPath := filepath.Join(t.TempDir(), "debian_apt.wasm")

	build := exec.Command("tinygo", "build", "-opt=z", "-buildmode=c-shared", "-o", wasmPath, "-target=wasi", "./guest/packs/debian/apt_repository")
	build.Dir = repoRoot
	build.Env = tinygoBuildEnv()
	output, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("tinygo build apt_repository: %v\n%s", err, output)
	}

	optimize := exec.Command("wasm-opt", "-Oz", wasmPath, "-o", wasmPath)
	optimize.Dir = repoRoot
	output, err = optimize.CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-opt -Oz apt_repository: %v\n%s", err, output)
	}

	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read built wasm: %v", err)
	}
	compressedWasm, err := plugincodec.CompressPluginModule(wasmBytes)
	if err != nil {
		t.Fatalf("CompressPluginModule() returned error: %v", err)
	}

	rt, err := NewWASMRuntime(context.Background(), capabilities.NewHostAPI(capabilities.HostProfile{
		DistroID:     "ubuntu",
		DistroName:   "Ubuntu",
		DistroFamily: "debian",
		PackageMgr:   "apt",
		InitSystem:   "systemd",
		Arch:         "amd64",
	}))
	if err != nil {
		t.Fatalf("new wasm runtime: %v", err)
	}
	defer func() {
		if closeErr := rt.Close(); closeErr != nil {
			t.Fatalf("close runtime: %v", closeErr)
		}
	}()
	dispatcher := NewDispatcherWithManifest(rt, testManifest(t, "debian_apt", wasmBytes, compressedWasm), nil)

	if _, err := dispatcher.LoadModule(hostrpc.ModuleLoadParams{
		Name:            "debian_apt",
		WasmCompression: assetmanifest.CompressionZstd,
		Wasm:            compressedWasm,
	}); err != nil {
		t.Fatalf("load compressed plugin: %v", err)
	}

	plan, err := json.Marshal(map[string]any{
		"resource_type": "apt_repository",
		"plan": map[string]any{
			"name":          "kubernetes",
			"uri":           "https://pkgs.k8s.io/core:/stable:/v1.30/deb/",
			"suite":         "/",
			"components":    []string{},
			"architectures": []string{},
			"signed_by":     "/etc/apt/keyrings/kubernetes-apt-keyring.gpg",
			"ensure":        "present",
			"update_cache":  false,
			"file_path":     filepath.Join(t.TempDir(), "kubernetes.list"),
		},
	})
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}

	result, err := rt.CallCreate(context.Background(), "debian_apt", plan)
	if err != nil {
		t.Fatalf("call create: %v", err)
	}

	var envelope struct {
		State map[string]any `json:"state"`
		Error string         `json:"error"`
	}
	if err := json.Unmarshal(result, &envelope); err != nil {
		t.Fatalf("unmarshal result: %v\nraw=%s", err, result)
	}
	if envelope.Error != "" {
		t.Fatalf("plugin returned error: %s", envelope.Error)
	}

	components, ok := envelope.State["components"].([]any)
	if !ok {
		t.Fatalf("components type = %T, want []any; raw=%s", envelope.State["components"], result)
	}
	if len(components) != 0 {
		t.Fatalf("components = %#v, want empty list", components)
	}

	architectures, ok := envelope.State["architectures"].([]any)
	if !ok {
		t.Fatalf("architectures type = %T, want []any; raw=%s", envelope.State["architectures"], result)
	}
	if len(architectures) != 0 {
		t.Fatalf("architectures = %#v, want empty list", architectures)
	}
}

func tinygoBuildEnv() []string {
	env := os.Environ()
	goRoot := "/opt/homebrew/opt/go@1.26/libexec"
	goBin := "/opt/homebrew/opt/go@1.26/bin"
	if _, err := os.Stat(goRoot); err != nil {
		return env
	}

	var out []string
	hasPath := false
	hasGoRoot := false
	for _, entry := range env {
		switch {
		case strings.HasPrefix(entry, "PATH="):
			out = append(out, "PATH="+goBin+":"+strings.TrimPrefix(entry, "PATH="))
			hasPath = true
		case strings.HasPrefix(entry, "GOROOT="):
			out = append(out, "GOROOT="+goRoot)
			hasGoRoot = true
		default:
			out = append(out, entry)
		}
	}
	if !hasPath {
		out = append(out, "PATH="+goBin)
	}
	if !hasGoRoot {
		out = append(out, "GOROOT="+goRoot)
	}
	return out
}
