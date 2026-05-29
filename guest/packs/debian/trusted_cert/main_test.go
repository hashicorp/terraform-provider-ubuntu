// Copyright IBM Corp. 2026

package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
)

func TestDesiredTrustedCertSpec(t *testing.T) {
	t.Parallel()

	certificate := testCACertificatePEM(t)
	spec, err := desiredTrustedCertSpec(pluginsdk.StateData{
		"name":        "Example Corp Root",
		"certificate": certificate,
	})
	if err != nil {
		t.Fatalf("desiredTrustedCertSpec returned error: %v", err)
	}
	if spec.Name != "example-corp-root" {
		t.Fatalf("unexpected name: %q", spec.Name)
	}
	if spec.Path != trustedCertDir+"/example-corp-root.crt" {
		t.Fatalf("unexpected path: %q", spec.Path)
	}
	if !strings.Contains(spec.Subject, "Example Corp Root") {
		t.Fatalf("unexpected subject: %q", spec.Subject)
	}
}

func TestNormalizeTrustedCertificateRejectsLeaf(t *testing.T) {
	t.Parallel()

	leaf := testLeafCertificatePEM(t)
	if _, _, _, err := normalizeTrustedCertificate(leaf); err == nil {
		t.Fatal("expected normalizeTrustedCertificate to reject non-CA cert")
	}
}

func TestSanitizeTrustedCertName(t *testing.T) {
	t.Parallel()

	if got := sanitizeTrustedCertName("Example Corp Root.crt"); got != "example-corp-root" {
		t.Fatalf("unexpected sanitized name: %q", got)
	}
}

func TestTrustedCertPathFallsBackToID(t *testing.T) {
	t.Parallel()

	path, err := trustedCertPath(pluginsdk.StateData{
		"id": trustedCertDir + "/imported-root.crt",
	})
	if err != nil {
		t.Fatalf("trustedCertPath returned error: %v", err)
	}
	if path != trustedCertDir+"/imported-root.crt" {
		t.Fatalf("unexpected path: %q", path)
	}
}

func TestCreateExistingTrustedCertRequiresImport(t *testing.T) {
	origGetHostProfile := pluginsdk.GetHostProfile
	origHasCommand := pluginsdk.HasCommand
	origFileExists := pluginsdk.FileExists
	t.Cleanup(func() {
		pluginsdk.GetHostProfile = origGetHostProfile
		pluginsdk.HasCommand = origHasCommand
		pluginsdk.FileExists = origFileExists
	})

	pluginsdk.GetHostProfile = func() (*pluginsdk.HostProfile, error) {
		return &pluginsdk.HostProfile{DistroFamily: "debian", AvailableCommands: []string{"update-ca-certificates"}}, nil
	}
	pluginsdk.HasCommand = func(name string) (bool, error) {
		return name == "update-ca-certificates", nil
	}
	pluginsdk.FileExists = func(path string) (bool, error) {
		return path == trustedCertDir+"/example-corp-root.crt", nil
	}

	_, err := (&trustedCertResource{}).Create(pluginsdk.StateData{
		"name":        "Example Corp Root",
		"certificate": testCACertificatePEM(t),
	})
	if err == nil || !strings.Contains(err.Error(), "import it before managing with terraform") {
		t.Fatalf("expected import-required error, got %v", err)
	}
}

func TestValidateAllowsUnknownCertificateDuringPlanning(t *testing.T) {
	origGetHostProfile := pluginsdk.GetHostProfile
	origHasCommand := pluginsdk.HasCommand
	t.Cleanup(func() {
		pluginsdk.GetHostProfile = origGetHostProfile
		pluginsdk.HasCommand = origHasCommand
	})

	pluginsdk.GetHostProfile = func() (*pluginsdk.HostProfile, error) {
		return &pluginsdk.HostProfile{DistroFamily: "debian", AvailableCommands: []string{"update-ca-certificates"}}, nil
	}
	pluginsdk.HasCommand = func(name string) (bool, error) {
		return name == "update-ca-certificates", nil
	}

	err := (&trustedCertResource{}).Validate(pluginsdk.StateData{
		"name": "Example Corp Root",
	})
	if err != nil {
		t.Fatalf("expected missing certificate to be treated as unknown plan input, got %v", err)
	}
}

func TestTrustedCertReadReturnsManagedState(t *testing.T) {
	origGetHostProfile := pluginsdk.GetHostProfile
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	t.Cleanup(func() {
		pluginsdk.GetHostProfile = origGetHostProfile
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
	})

	certificate := testCACertificatePEM(t)
	path := trustedCertDir + "/example-corp-root.crt"
	pluginsdk.GetHostProfile = func() (*pluginsdk.HostProfile, error) {
		return debianTrustedCertProfile(), nil
	}
	pluginsdk.FileExists = func(candidate string) (bool, error) {
		return candidate == path, nil
	}
	pluginsdk.FileRead = func(candidate string) ([]byte, error) {
		if candidate != path {
			t.Fatalf("unexpected read path: %q", candidate)
		}
		return []byte(certificate), nil
	}

	state, err := (&trustedCertResource{}).Read(pluginsdk.StateData{"name": "Example Corp Root"})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if state == nil {
		t.Fatal("expected managed trusted cert state")
	}
	if got := state.GetString("id"); got != path {
		t.Fatalf("id = %q, want %q", got, path)
	}
	if got := state.GetString("name"); got != "example-corp-root" {
		t.Fatalf("name = %q, want example-corp-root", got)
	}
	if got := state.GetString("cert_path"); got != path {
		t.Fatalf("cert_path = %q, want %q", got, path)
	}
	if got := state.GetString("certificate"); got == "" || strings.HasSuffix(got, "\n") {
		t.Fatalf("expected normalized certificate without trailing newline, got %q", got)
	}
	if got := state.GetString("digest"); !strings.HasPrefix(got, "blake3:") {
		t.Fatalf("unexpected digest: %q", got)
	}
	if got := state.GetString("subject"); !strings.Contains(got, "Example Corp Root") {
		t.Fatalf("unexpected subject: %q", got)
	}
	if got := state.GetString("issuer"); !strings.Contains(got, "Example Corp Root") {
		t.Fatalf("unexpected issuer: %q", got)
	}
}

func TestTrustedCertReadReturnsNilWhenMissing(t *testing.T) {
	origGetHostProfile := pluginsdk.GetHostProfile
	origFileExists := pluginsdk.FileExists
	t.Cleanup(func() {
		pluginsdk.GetHostProfile = origGetHostProfile
		pluginsdk.FileExists = origFileExists
	})

	pluginsdk.GetHostProfile = func() (*pluginsdk.HostProfile, error) {
		return debianTrustedCertProfile(), nil
	}
	pluginsdk.FileExists = func(string) (bool, error) {
		return false, nil
	}

	state, err := (&trustedCertResource{}).Read(pluginsdk.StateData{"name": "Example Corp Root"})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if state != nil {
		t.Fatalf("expected nil state for missing trusted cert, got %#v", state)
	}
}

func TestTrustedCertUpdateWritesCertificateAndRefreshes(t *testing.T) {
	origGetHostProfile := pluginsdk.GetHostProfile
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origFileWrite := pluginsdk.FileWrite
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.GetHostProfile = origGetHostProfile
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.CmdExec = origCmdExec
	})

	certificate := testCACertificatePEM(t)
	plan := pluginsdk.StateData{
		"name":        "Example Corp Root",
		"certificate": certificate,
	}
	path := trustedCertDir + "/example-corp-root.crt"
	pluginsdk.GetHostProfile = func() (*pluginsdk.HostProfile, error) {
		return debianTrustedCertProfile(), nil
	}
	pluginsdk.FileExists = func(candidate string) (bool, error) {
		if candidate != path {
			t.Fatalf("unexpected exists path: %q", candidate)
		}
		return false, nil
	}
	pluginsdk.FileRead = func(candidate string) ([]byte, error) {
		if candidate != path {
			t.Fatalf("unexpected read path: %q", candidate)
		}
		return nil, errors.New("missing trusted cert")
	}
	var wrotePath string
	var wroteData []byte
	var wroteMode uint32
	pluginsdk.FileWrite = func(candidate string, data []byte, mode uint32) error {
		wrotePath = candidate
		wroteData = append([]byte(nil), data...)
		wroteMode = mode
		return nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		if cmd != "update-ca-certificates" {
			t.Fatalf("unexpected command: %s %#v", cmd, args)
		}
		if len(args) != 0 {
			t.Fatalf("unexpected update-ca-certificates args: %#v", args)
		}
		return &pluginsdk.CmdResult{ExitCode: 0}, nil
	}

	state, err := (&trustedCertResource{}).Update(nil, plan)
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if wrotePath != path {
		t.Fatalf("wrote path = %q, want %q", wrotePath, path)
	}
	if wroteMode != 0o644 {
		t.Fatalf("wrote mode = %#o, want %#o", wroteMode, uint32(0o644))
	}
	if len(wroteData) == 0 || strings.HasSuffix(string(wroteData), "\n") {
		t.Fatalf("expected normalized written certificate without trailing newline, got %q", string(wroteData))
	}
	if got := state.GetString("id"); got != path {
		t.Fatalf("id = %q, want %q", got, path)
	}
	if got := state.GetString("digest"); !strings.HasPrefix(got, "blake3:") {
		t.Fatalf("unexpected digest: %q", got)
	}
	if got := state.GetString("certificate"); got != string(wroteData) {
		t.Fatalf("certificate = %q, want written certificate", got)
	}
}

func TestTrustedCertDeleteRemovesCertificateAndRefreshes(t *testing.T) {
	origGetHostProfile := pluginsdk.GetHostProfile
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origFileDelete := pluginsdk.FileDelete
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.GetHostProfile = origGetHostProfile
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileDelete = origFileDelete
		pluginsdk.CmdExec = origCmdExec
	})

	certificate := testCACertificatePEM(t)
	path := trustedCertDir + "/example-corp-root.crt"
	pluginsdk.GetHostProfile = func() (*pluginsdk.HostProfile, error) {
		return debianTrustedCertProfile(), nil
	}
	pluginsdk.FileExists = func(candidate string) (bool, error) {
		if candidate != path {
			t.Fatalf("unexpected exists path: %q", candidate)
		}
		return true, nil
	}
	pluginsdk.FileRead = func(candidate string) ([]byte, error) {
		if candidate != path {
			t.Fatalf("unexpected read path: %q", candidate)
		}
		return []byte(certificate), nil
	}
	var deletedPath string
	pluginsdk.FileDelete = func(candidate string) error {
		deletedPath = candidate
		return nil
	}
	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		if cmd != "update-ca-certificates" {
			t.Fatalf("unexpected command: %s %#v", cmd, args)
		}
		return &pluginsdk.CmdResult{ExitCode: 0}, nil
	}

	err := (&trustedCertResource{}).Delete(pluginsdk.StateData{"cert_path": path})
	if err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if deletedPath != path {
		t.Fatalf("deleted path = %q, want %q", deletedPath, path)
	}
}

func TestTrustedCertImportStateReadsManagedCertificate(t *testing.T) {
	origGetHostProfile := pluginsdk.GetHostProfile
	origFileRead := pluginsdk.FileRead
	t.Cleanup(func() {
		pluginsdk.GetHostProfile = origGetHostProfile
		pluginsdk.FileRead = origFileRead
	})

	certificate := testCACertificatePEM(t)
	path := trustedCertDir + "/example-corp-root.crt"
	pluginsdk.GetHostProfile = func() (*pluginsdk.HostProfile, error) {
		return debianTrustedCertProfile(), nil
	}
	pluginsdk.FileRead = func(candidate string) ([]byte, error) {
		if candidate != path {
			t.Fatalf("unexpected read path: %q", candidate)
		}
		return []byte(certificate), nil
	}

	state, err := (&trustedCertResource{}).ImportState("Example Corp Root")
	if err != nil {
		t.Fatalf("ImportState returned error: %v", err)
	}
	if got := state.GetString("id"); got != path {
		t.Fatalf("id = %q, want %q", got, path)
	}
	if got := state.GetString("name"); got != "example-corp-root" {
		t.Fatalf("name = %q, want example-corp-root", got)
	}
	if got := state.GetString("cert_path"); got != path {
		t.Fatalf("cert_path = %q, want %q", got, path)
	}
}

func TestEnsureDebianTrustStoreRejectsUnsupportedHosts(t *testing.T) {
	origGetHostProfile := pluginsdk.GetHostProfile
	t.Cleanup(func() {
		pluginsdk.GetHostProfile = origGetHostProfile
	})

	pluginsdk.GetHostProfile = func() (*pluginsdk.HostProfile, error) {
		return &pluginsdk.HostProfile{DistroFamily: "redhat", AvailableCommands: []string{"update-ca-certificates"}}, nil
	}
	if err := ensureDebianTrustStore(); err == nil || !strings.Contains(err.Error(), "Debian-family") {
		t.Fatalf("expected Debian-family error, got %v", err)
	}

	pluginsdk.GetHostProfile = func() (*pluginsdk.HostProfile, error) {
		return &pluginsdk.HostProfile{DistroFamily: "debian"}, nil
	}
	if err := ensureDebianTrustStore(); err == nil || !strings.Contains(err.Error(), "update-ca-certificates") {
		t.Fatalf("expected missing update-ca-certificates error, got %v", err)
	}
}

func TestUpdateTrustStoreReturnsExitStatus(t *testing.T) {
	origCmdExec := pluginsdk.CmdExec
	t.Cleanup(func() {
		pluginsdk.CmdExec = origCmdExec
	})

	pluginsdk.CmdExec = func(cmd string, args []string) (*pluginsdk.CmdResult, error) {
		if cmd != "update-ca-certificates" {
			t.Fatalf("unexpected command: %s %#v", cmd, args)
		}
		return &pluginsdk.CmdResult{ExitCode: 2, Stderr: "refresh failed"}, nil
	}

	err := updateTrustStore()
	if err == nil || !strings.Contains(err.Error(), "exit 2") || !strings.Contains(err.Error(), "refresh failed") {
		t.Fatalf("expected exit status error, got %v", err)
	}
}

func debianTrustedCertProfile() *pluginsdk.HostProfile {
	return &pluginsdk.HostProfile{DistroFamily: "debian", AvailableCommands: []string{"update-ca-certificates"}}
}

func testCACertificatePEM(t *testing.T) string {
	t.Helper()
	return testCertificatePEM(t, true, "Example Corp Root")
}

func testLeafCertificatePEM(t *testing.T) string {
	t.Helper()
	return testCertificatePEM(t, false, "leaf.example.internal")
}

func testCertificatePEM(t *testing.T, isCA bool, commonName string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  isCA,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature,
	}
	if isCA {
		tpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
