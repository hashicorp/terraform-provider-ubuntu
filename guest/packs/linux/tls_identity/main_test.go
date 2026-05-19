package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
)

func TestDesiredTLSIdentitySpecPEMFullchain(t *testing.T) {
	t.Parallel()

	material := testTLSMaterial(t)
	spec, family, err := desiredTLSIdentitySpec(pluginsdk.StateData{
		"name":            "api.example.internal",
		"fullchain_pem":   material.FullchainPEM,
		"private_key_pem": material.PrivateKeyPEM,
	})
	if err != nil {
		t.Fatalf("desiredTLSIdentitySpec returned error: %v", err)
	}
	if family != tlsIdentityFamilyPEMFullchain {
		t.Fatalf("unexpected family: %q", family)
	}
	if spec.FullchainPath != tlsIdentityFullchainDir+"/api.example.internal-fullchain.pem" {
		t.Fatalf("unexpected fullchain path: %q", spec.FullchainPath)
	}
	if spec.PrivateKeyPath != tlsIdentityPrivateDir+"/api.example.internal.key" {
		t.Fatalf("unexpected key path: %q", spec.PrivateKeyPath)
	}
	if !strings.Contains(spec.PrivateKeyPEM, "BEGIN PRIVATE KEY") {
		t.Fatalf("expected canonical PKCS#8 private key PEM, got %q", spec.PrivateKeyPEM)
	}
}

func TestDesiredTLSIdentitySpecPEMSplit(t *testing.T) {
	t.Parallel()

	material := testTLSMaterial(t)
	spec, family, err := desiredTLSIdentitySpec(pluginsdk.StateData{
		"name":            "api.example.internal",
		"certificate_pem": material.LeafPEM,
		"ca_chain_pem":    material.ChainPEM,
		"private_key_pem": material.PrivateKeyPEM,
	})
	if err != nil {
		t.Fatalf("desiredTLSIdentitySpec returned error: %v", err)
	}
	if family != tlsIdentityFamilyPEMSplit {
		t.Fatalf("unexpected family: %q", family)
	}
	if len(spec.Certificates) != 2 {
		t.Fatalf("expected 2 certificates, got %d", len(spec.Certificates))
	}
}

func TestDesiredTLSIdentitySpecDERFullchain(t *testing.T) {
	t.Parallel()

	material := testTLSMaterial(t)
	spec, family, err := desiredTLSIdentitySpec(pluginsdk.StateData{
		"name":                   "api.example.internal",
		"fullchain_der_base64":   material.FullchainDERBase64,
		"private_key_der_base64": material.PrivateKeyDERBase64,
	})
	if err != nil {
		t.Fatalf("desiredTLSIdentitySpec returned error: %v", err)
	}
	if family != tlsIdentityFamilyDERFullchain {
		t.Fatalf("unexpected family: %q", family)
	}
	if len(spec.Certificates) != 2 {
		t.Fatalf("expected 2 certificates, got %d", len(spec.Certificates))
	}
}

func TestDesiredTLSIdentitySpecDERSplit(t *testing.T) {
	t.Parallel()

	material := testTLSMaterial(t)
	spec, family, err := desiredTLSIdentitySpec(pluginsdk.StateData{
		"name":                   "api.example.internal",
		"certificate_der_base64": material.LeafDERBase64,
		"ca_chain_der_base64":    material.ChainDERBase64,
		"private_key_der_base64": material.PrivateKeyDERBase64,
	})
	if err != nil {
		t.Fatalf("desiredTLSIdentitySpec returned error: %v", err)
	}
	if family != tlsIdentityFamilyDERSplit {
		t.Fatalf("unexpected family: %q", family)
	}
	if len(spec.Certificates) != 2 {
		t.Fatalf("expected 2 certificates, got %d", len(spec.Certificates))
	}
}

func TestDesiredTLSIdentitySpecRejectsMixedFamilies(t *testing.T) {
	t.Parallel()

	material := testTLSMaterial(t)
	if _, _, err := desiredTLSIdentitySpec(pluginsdk.StateData{
		"name":                 "api.example.internal",
		"certificate_pem":      material.LeafPEM,
		"private_key_pem":      material.PrivateKeyPEM,
		"fullchain_der_base64": material.FullchainDERBase64,
	}); err == nil {
		t.Fatal("expected mixed input families to be rejected")
	}
}

func TestDesiredTLSIdentitySpecRejectsMismatchedKey(t *testing.T) {
	t.Parallel()

	material := testTLSMaterial(t)
	other := testTLSMaterial(t)
	if _, _, err := desiredTLSIdentitySpec(pluginsdk.StateData{
		"name":            "api.example.internal",
		"fullchain_pem":   material.FullchainPEM,
		"private_key_pem": other.PrivateKeyPEM,
	}); err == nil {
		t.Fatal("expected mismatched key to be rejected")
	}
}

func TestTLSIdentityStateUsesRequestedFamily(t *testing.T) {
	t.Parallel()

	material := testTLSMaterial(t)
	spec, _, err := desiredTLSIdentitySpec(pluginsdk.StateData{
		"name":            "api.example.internal",
		"fullchain_pem":   material.FullchainPEM,
		"private_key_pem": material.PrivateKeyPEM,
	})
	if err != nil {
		t.Fatalf("desiredTLSIdentitySpec returned error: %v", err)
	}
	state := tlsIdentityState(spec, tlsIdentityFamilyDERSplit)
	if _, ok := state["certificate_der_base64"]; !ok {
		t.Fatal("expected der split certificate state")
	}
	if _, ok := state["private_key_der_base64"]; !ok {
		t.Fatal("expected der split key state")
	}
	if _, ok := state["fullchain_pem"]; ok {
		t.Fatal("did not expect pem fullchain state for der split family")
	}
}

func TestTLSIdentityNameFromImportID(t *testing.T) {
	t.Parallel()

	name, err := tlsIdentityNameFromImportID("/etc/ssl/certs/demo.internal-fullchain.pem")
	if err != nil {
		t.Fatalf("tlsIdentityNameFromImportID returned error: %v", err)
	}
	if name != "demo.internal" {
		t.Fatalf("unexpected import name: %q", name)
	}
}

func TestNormalizeTLSIdentityNameRejectsPathLike(t *testing.T) {
	t.Parallel()

	if _, err := normalizeTLSIdentityName("../bad"); err == nil {
		t.Fatal("expected path-like name to be rejected")
	}
}

func TestCreateExistingTLSIdentityRequiresImport(t *testing.T) {
	material := testTLSMaterial(t)

	origGetHostProfile := pluginsdk.GetHostProfile
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	t.Cleanup(func() {
		pluginsdk.GetHostProfile = origGetHostProfile
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
	})

	pluginsdk.GetHostProfile = func() (*pluginsdk.HostProfile, error) {
		return &pluginsdk.HostProfile{DistroFamily: "debian"}, nil
	}
	pluginsdk.FileExists = func(path string) (bool, error) {
		return true, nil
	}
	pluginsdk.FileRead = func(path string) ([]byte, error) {
		if strings.HasSuffix(path, "-fullchain.pem") {
			return []byte(material.FullchainPEM), nil
		}
		return []byte(material.PrivateKeyPEM), nil
	}

	_, err := (&tlsIdentityResource{}).Create(pluginsdk.StateData{
		"name":            "api.example.internal",
		"fullchain_pem":   material.FullchainPEM,
		"private_key_pem": material.PrivateKeyPEM,
	})
	if err == nil || !strings.Contains(err.Error(), "import it before managing with terraform") {
		t.Fatalf("expected import-required error, got %v", err)
	}
}

func TestTLSIdentityResourceLifecycle(t *testing.T) {
	material := testTLSMaterial(t)

	origGetHostProfile := pluginsdk.GetHostProfile
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origFileWrite := pluginsdk.FileWrite
	origFileDelete := pluginsdk.FileDelete
	origFileChown := pluginsdk.FileChown
	origFileChmod := pluginsdk.FileChmod
	origFileStat := pluginsdk.FileStat_
	t.Cleanup(func() {
		pluginsdk.GetHostProfile = origGetHostProfile
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.FileDelete = origFileDelete
		pluginsdk.FileChown = origFileChown
		pluginsdk.FileChmod = origFileChmod
		pluginsdk.FileStat_ = origFileStat
	})

	fs := newFakeTLSIdentityFS(t)
	pluginsdk.GetHostProfile = func() (*pluginsdk.HostProfile, error) {
		return &pluginsdk.HostProfile{DistroFamily: "debian"}, nil
	}
	pluginsdk.FileExists = fs.fileExists
	pluginsdk.FileRead = fs.fileRead
	pluginsdk.FileWrite = fs.fileWrite
	pluginsdk.FileDelete = fs.fileDelete
	pluginsdk.FileChown = fs.fileChown
	pluginsdk.FileChmod = fs.fileChmod
	pluginsdk.FileStat_ = fs.fileStat

	plan := pluginsdk.StateData{
		"name":            "api.example.internal",
		"fullchain_pem":   material.FullchainPEM,
		"private_key_pem": material.PrivateKeyPEM,
	}
	if err := (&tlsIdentityResource{}).Validate(plan); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	created, err := (&tlsIdentityResource{}).Create(plan)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	fullchainPath := tlsIdentityFullchainPath("api.example.internal")
	privateKeyPath := tlsIdentityPrivateKeyPath("api.example.internal")
	if created.GetString("id") != "api.example.internal" {
		t.Fatalf("unexpected created state: %#v", created)
	}
	if !fs.hasFile(fullchainPath) || !fs.hasFile(privateKeyPath) {
		t.Fatalf("expected managed TLS files to be written")
	}
	if fs.files[fullchainPath].mode != 0o644 || fs.files[privateKeyPath].mode != 0o600 {
		t.Fatalf("unexpected managed file modes: %#v %#v", fs.files[fullchainPath], fs.files[privateKeyPath])
	}
	if fs.files[fullchainPath].owner != "root" || fs.files[privateKeyPath].group != "root" {
		t.Fatalf("expected managed TLS files to be root-owned: %#v %#v", fs.files[fullchainPath], fs.files[privateKeyPath])
	}

	readState, err := (&tlsIdentityResource{}).Read(pluginsdk.StateData{
		"fullchain_path":  fullchainPath,
		"certificate_pem": "known",
	})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if readState.GetString("id") != "api.example.internal" {
		t.Fatalf("unexpected read state: %#v", readState)
	}
	if _, ok := readState["certificate_pem"]; !ok {
		t.Fatalf("expected split PEM state from prior family hint: %#v", readState)
	}
	if _, ok := readState["ca_chain_pem"]; !ok {
		t.Fatalf("expected CA chain state from split PEM family: %#v", readState)
	}
	if _, ok := readState["fullchain_pem"]; ok {
		t.Fatalf("did not expect fullchain_pem in split PEM state: %#v", readState)
	}

	imported, err := (&tlsIdentityResource{}).ImportState(fullchainPath)
	if err != nil {
		t.Fatalf("ImportState returned error: %v", err)
	}
	if imported.GetString("id") != "api.example.internal" {
		t.Fatalf("unexpected imported state: %#v", imported)
	}

	err = (&tlsIdentityResource{}).Delete(pluginsdk.StateData{"private_key_path": privateKeyPath})
	if err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if fs.hasFile(fullchainPath) || fs.hasFile(privateKeyPath) {
		t.Fatalf("expected managed TLS files to be removed")
	}
}

func TestTLSIdentityUpdateRewritesManagedFiles(t *testing.T) {
	material := testTLSMaterial(t)
	updatedMaterial := testTLSMaterial(t)

	origGetHostProfile := pluginsdk.GetHostProfile
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origFileWrite := pluginsdk.FileWrite
	origFileDelete := pluginsdk.FileDelete
	origFileChown := pluginsdk.FileChown
	origFileChmod := pluginsdk.FileChmod
	origFileStat := pluginsdk.FileStat_
	t.Cleanup(func() {
		pluginsdk.GetHostProfile = origGetHostProfile
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.FileDelete = origFileDelete
		pluginsdk.FileChown = origFileChown
		pluginsdk.FileChmod = origFileChmod
		pluginsdk.FileStat_ = origFileStat
	})

	fs := newFakeTLSIdentityFS(t)
	pluginsdk.GetHostProfile = func() (*pluginsdk.HostProfile, error) {
		return &pluginsdk.HostProfile{DistroFamily: "debian"}, nil
	}
	pluginsdk.FileExists = fs.fileExists
	pluginsdk.FileRead = fs.fileRead
	pluginsdk.FileWrite = fs.fileWrite
	pluginsdk.FileDelete = fs.fileDelete
	pluginsdk.FileChown = fs.fileChown
	pluginsdk.FileChmod = fs.fileChmod
	pluginsdk.FileStat_ = fs.fileStat

	name := "api.example.internal"
	fullchainPath := tlsIdentityFullchainPath(name)
	privateKeyPath := tlsIdentityPrivateKeyPath(name)
	fs.seedFile(fullchainPath, material.FullchainPEM, 0o644, "svc", "svc")
	fs.seedFile(privateKeyPath, material.PrivateKeyPEM, 0o600, "svc", "svc")

	updated, err := (&tlsIdentityResource{}).Update(nil, pluginsdk.StateData{
		"name":            name,
		"fullchain_pem":   updatedMaterial.FullchainPEM,
		"private_key_pem": updatedMaterial.PrivateKeyPEM,
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if updated.GetString("id") != name {
		t.Fatalf("unexpected updated state: %#v", updated)
	}
	if fs.files[fullchainPath].content == material.FullchainPEM || fs.files[privateKeyPath].content == material.PrivateKeyPEM {
		t.Fatalf("expected managed TLS files to be rewritten")
	}
	if fs.files[fullchainPath].owner != "root" || fs.files[privateKeyPath].group != "root" {
		t.Fatalf("expected rewritten TLS files to be root-owned: %#v %#v", fs.files[fullchainPath], fs.files[privateKeyPath])
	}
}

func TestApplyTLSIdentityRestoresSnapshotOnFailure(t *testing.T) {
	material := testTLSMaterial(t)
	updatedMaterial := testTLSMaterial(t)

	origGetHostProfile := pluginsdk.GetHostProfile
	origFileExists := pluginsdk.FileExists
	origFileRead := pluginsdk.FileRead
	origFileWrite := pluginsdk.FileWrite
	origFileDelete := pluginsdk.FileDelete
	origFileChown := pluginsdk.FileChown
	origFileChmod := pluginsdk.FileChmod
	origFileStat := pluginsdk.FileStat_
	t.Cleanup(func() {
		pluginsdk.GetHostProfile = origGetHostProfile
		pluginsdk.FileExists = origFileExists
		pluginsdk.FileRead = origFileRead
		pluginsdk.FileWrite = origFileWrite
		pluginsdk.FileDelete = origFileDelete
		pluginsdk.FileChown = origFileChown
		pluginsdk.FileChmod = origFileChmod
		pluginsdk.FileStat_ = origFileStat
	})

	fs := newFakeTLSIdentityFS(t)
	pluginsdk.GetHostProfile = func() (*pluginsdk.HostProfile, error) {
		return &pluginsdk.HostProfile{DistroFamily: "debian"}, nil
	}
	pluginsdk.FileExists = fs.fileExists
	pluginsdk.FileRead = fs.fileRead
	pluginsdk.FileWrite = fs.fileWrite
	pluginsdk.FileDelete = fs.fileDelete
	pluginsdk.FileChown = fs.fileChown
	pluginsdk.FileChmod = fs.fileChmod
	pluginsdk.FileStat_ = fs.fileStat

	name := "api.example.internal"
	fullchainPath := tlsIdentityFullchainPath(name)
	privateKeyPath := tlsIdentityPrivateKeyPath(name)
	fs.seedFile(fullchainPath, material.FullchainPEM, 0o644, "svc", "svc")
	fs.seedFile(privateKeyPath, material.PrivateKeyPEM, 0o600, "svc", "svc")
	fs.failNextWritePath = privateKeyPath
	fs.failNextWriteErr = errors.New("simulated private key write failure")

	_, err := applyTLSIdentity(pluginsdk.StateData{
		"name":            name,
		"fullchain_pem":   updatedMaterial.FullchainPEM,
		"private_key_pem": updatedMaterial.PrivateKeyPEM,
	})
	if err == nil || !strings.Contains(err.Error(), "simulated private key write failure") {
		t.Fatalf("expected reconcile failure, got %v", err)
	}
	if fs.files[fullchainPath].content != material.FullchainPEM || fs.files[privateKeyPath].content != material.PrivateKeyPEM {
		t.Fatalf("expected snapshot restore after reconcile failure: %#v %#v", fs.files[fullchainPath], fs.files[privateKeyPath])
	}
	if fs.files[fullchainPath].owner != "svc" || fs.files[privateKeyPath].group != "svc" {
		t.Fatalf("expected snapshot ownership restore after failure: %#v %#v", fs.files[fullchainPath], fs.files[privateKeyPath])
	}
}

type fakeTLSIdentityFile struct {
	content string
	mode    uint32
	owner   string
	group   string
}

type fakeTLSIdentityFS struct {
	t                 *testing.T
	files             map[string]*fakeTLSIdentityFile
	failNextWritePath string
	failNextWriteErr  error
}

func newFakeTLSIdentityFS(t *testing.T) *fakeTLSIdentityFS {
	t.Helper()
	return &fakeTLSIdentityFS{t: t, files: map[string]*fakeTLSIdentityFile{}}
}

func (f *fakeTLSIdentityFS) seedFile(path, content string, mode uint32, owner, group string) {
	f.files[path] = &fakeTLSIdentityFile{content: content, mode: mode, owner: owner, group: group}
}

func (f *fakeTLSIdentityFS) hasFile(path string) bool {
	_, ok := f.files[path]
	return ok
}

func (f *fakeTLSIdentityFS) fileExists(path string) (bool, error) {
	return f.hasFile(path), nil
}

func (f *fakeTLSIdentityFS) fileRead(path string) ([]byte, error) {
	file, ok := f.files[path]
	if !ok {
		return nil, errors.New("missing file")
	}
	return []byte(file.content), nil
}

func (f *fakeTLSIdentityFS) fileWrite(path string, data []byte, mode uint32) error {
	if f.failNextWriteErr != nil && path == f.failNextWritePath {
		err := f.failNextWriteErr
		f.failNextWritePath = ""
		f.failNextWriteErr = nil
		return err
	}
	file := f.files[path]
	if file == nil {
		file = &fakeTLSIdentityFile{}
		f.files[path] = file
	}
	file.content = string(data)
	file.mode = mode
	return nil
}

func (f *fakeTLSIdentityFS) fileDelete(path string) error {
	delete(f.files, path)
	return nil
}

func (f *fakeTLSIdentityFS) fileChown(path string, owner, group string) error {
	file := f.files[path]
	if file == nil {
		return errors.New("missing file for chown")
	}
	file.owner = owner
	file.group = group
	return nil
}

func (f *fakeTLSIdentityFS) fileChmod(path string, mode uint32) error {
	file := f.files[path]
	if file == nil {
		return errors.New("missing file for chmod")
	}
	file.mode = mode
	return nil
}

func (f *fakeTLSIdentityFS) fileStat(path string) (*pluginsdk.FileStat, error) {
	file := f.files[path]
	if file == nil {
		return nil, errors.New("missing file for stat")
	}
	return &pluginsdk.FileStat{Path: path, Mode: file.mode, Owner: file.owner, Group: file.group}, nil
}

type tlsTestMaterial struct {
	LeafPEM             string
	ChainPEM            string
	FullchainPEM        string
	PrivateKeyPEM       string
	LeafDERBase64       string
	ChainDERBase64      string
	FullchainDERBase64  string
	PrivateKeyDERBase64 string
}

func testTLSMaterial(t *testing.T) tlsTestMaterial {
	t.Helper()

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}

	caTpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "Example Root CA",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}

	leafTpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName: "api.example.internal",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"api.example.internal"},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf certificate: %v", err)
	}

	leafPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}))
	chainPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	fullchainPEM := strings.TrimRight(leafPEM, "\n") + "\n" + strings.TrimRight(chainPEM, "\n")
	privateKeyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(leafKey)}))

	return tlsTestMaterial{
		LeafPEM:             leafPEM,
		ChainPEM:            chainPEM,
		FullchainPEM:        fullchainPEM,
		PrivateKeyPEM:       privateKeyPEM,
		LeafDERBase64:       base64.StdEncoding.EncodeToString(leafDER),
		ChainDERBase64:      base64.StdEncoding.EncodeToString(caDER),
		FullchainDERBase64:  base64.StdEncoding.EncodeToString(append(append([]byte{}, leafDER...), caDER...)),
		PrivateKeyDERBase64: base64.StdEncoding.EncodeToString(x509.MarshalPKCS1PrivateKey(leafKey)),
	}
}
