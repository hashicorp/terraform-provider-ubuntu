package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
	linuxtlscontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/linuxtls"
	digestutil "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/digest"
)

const (
	tlsIdentityFullchainDir = "/etc/ssl/certs"
	tlsIdentityPrivateDir   = "/etc/ssl/private"
)

type tlsIdentityInputFamily string

const (
	tlsIdentityFamilyPEMFullchain tlsIdentityInputFamily = "pem_fullchain"
	tlsIdentityFamilyPEMSplit     tlsIdentityInputFamily = "pem_split"
	tlsIdentityFamilyDERFullchain tlsIdentityInputFamily = "der_fullchain"
	tlsIdentityFamilyDERSplit     tlsIdentityInputFamily = "der_split"
)

type tlsIdentityResource struct{}

type tlsIdentitySpec struct {
	Name             string
	Certificates     []*x509.Certificate
	FullchainPEM     string
	PrivateKeyPEM    string
	PrivateKeyDER    []byte
	FullchainPath    string
	PrivateKeyPath   string
	Subject          string
	Issuer           string
	SerialNumber     string
	NotAfter         string
	FullchainDigest  string
	PrivateKeyDigest string
}

type tlsIdentitySnapshot struct {
	Fullchain  managedFileSnapshot
	PrivateKey managedFileSnapshot
}

type managedFileSnapshot struct {
	Path    string
	Exists  bool
	Content string
	Mode    uint32
	Owner   string
	Group   string
}

func (r *tlsIdentityResource) Name() string { return "tls_identity" }

func (r *tlsIdentityResource) Schema() pluginsdk.Schema {
	return linuxtlscontract.TLSIdentityResourceSchema()
}

func (r *tlsIdentityResource) Validate(config pluginsdk.StateData) error {
	if err := ensureLinuxTLSIdentity(); err != nil {
		return err
	}
	_, _, err := desiredTLSIdentitySpec(config)
	return err
}

func (r *tlsIdentityResource) Read(state pluginsdk.StateData) (pluginsdk.StateData, error) {
	if err := ensureLinuxTLSIdentity(); err != nil {
		return nil, err
	}
	name, err := tlsIdentityNameFromState(state)
	if err != nil {
		return nil, err
	}
	return readTLSIdentityState(name, state)
}

func (r *tlsIdentityResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	if err := ensureLinuxTLSIdentity(); err != nil {
		return nil, err
	}

	name, err := normalizeTLSIdentityName(plan.GetString("name"))
	if err != nil {
		return nil, err
	}

	existing, err := readTLSIdentityState(name, pluginsdk.StateData{"input_family": string(tlsIdentityFamilyPEMFullchain)})
	if err == nil && existing != nil {
		return nil, fmt.Errorf("tls identity %q already exists; import it before managing with terraform", name)
	}
	if err != nil {
		fullchainExists, fullchainErr := pluginsdk.FileExists(tlsIdentityFullchainPath(name))
		if fullchainErr != nil {
			return nil, fmt.Errorf("check fullchain %s: %w", tlsIdentityFullchainPath(name), fullchainErr)
		}
		privateKeyExists, privateKeyErr := pluginsdk.FileExists(tlsIdentityPrivateKeyPath(name))
		if privateKeyErr != nil {
			return nil, fmt.Errorf("check private key %s: %w", tlsIdentityPrivateKeyPath(name), privateKeyErr)
		}
		if fullchainExists || privateKeyExists {
			return nil, fmt.Errorf("tls identity %q already exists in incomplete form; import it or clean it up before managing with terraform", name)
		}
	}

	return applyTLSIdentity(plan)
}

func (r *tlsIdentityResource) Update(_ pluginsdk.StateData, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applyTLSIdentity(plan)
}

func (r *tlsIdentityResource) Delete(state pluginsdk.StateData) error {
	if err := ensureLinuxTLSIdentity(); err != nil {
		return err
	}
	name, err := tlsIdentityNameFromState(state)
	if err != nil {
		return err
	}
	return removeTLSIdentity(name)
}

func (r *tlsIdentityResource) ImportState(id string) (pluginsdk.StateData, error) {
	if err := ensureLinuxTLSIdentity(); err != nil {
		return nil, err
	}
	name, err := tlsIdentityNameFromImportID(id)
	if err != nil {
		return nil, err
	}
	return readTLSIdentityState(name, pluginsdk.StateData{"input_family": string(tlsIdentityFamilyPEMFullchain)})
}

func applyTLSIdentity(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	if err := ensureLinuxTLSIdentity(); err != nil {
		return nil, err
	}

	spec, family, err := desiredTLSIdentitySpec(plan)
	if err != nil {
		return nil, err
	}

	snapshot, err := captureTLSIdentitySnapshot(spec)
	if err != nil {
		return nil, err
	}

	if err := reconcileTLSIdentity(spec, snapshot); err != nil {
		if restoreErr := restoreTLSIdentity(snapshot); restoreErr != nil {
			return nil, fmt.Errorf("%w (restore failed: %v)", err, restoreErr)
		}
		return nil, err
	}

	return tlsIdentityState(spec, family), nil
}

func readTLSIdentityState(name string, prior pluginsdk.StateData) (pluginsdk.StateData, error) {
	fullchainPath := tlsIdentityFullchainPath(name)
	privateKeyPath := tlsIdentityPrivateKeyPath(name)

	fullchainExists, err := pluginsdk.FileExists(fullchainPath)
	if err != nil {
		return nil, fmt.Errorf("check fullchain %s: %w", fullchainPath, err)
	}
	privateKeyExists, err := pluginsdk.FileExists(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("check private key %s: %w", privateKeyPath, err)
	}
	if !fullchainExists && !privateKeyExists {
		return nil, nil
	}
	if fullchainExists != privateKeyExists {
		return nil, fmt.Errorf("tls identity %q is incomplete: expected both %s and %s", name, fullchainPath, privateKeyPath)
	}

	fullchainData, err := pluginsdk.FileRead(fullchainPath)
	if err != nil {
		return nil, fmt.Errorf("read fullchain %s: %w", fullchainPath, err)
	}
	privateKeyData, err := pluginsdk.FileRead(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read private key %s: %w", privateKeyPath, err)
	}

	spec, err := parseTLSIdentityPEMFullchainSpec(name, string(fullchainData), string(privateKeyData))
	if err != nil {
		return nil, err
	}

	return tlsIdentityState(spec, tlsIdentityFamilyFromState(prior)), nil
}

func desiredTLSIdentitySpec(data pluginsdk.StateData) (*tlsIdentitySpec, tlsIdentityInputFamily, error) {
	name, err := normalizeTLSIdentityName(data.GetString("name"))
	if err != nil {
		return nil, "", err
	}
	family, err := detectTLSIdentityInputFamily(data)
	if err != nil {
		return nil, "", err
	}
	spec, err := parseTLSIdentitySpecForFamily(name, family, data)
	if err != nil {
		return nil, "", err
	}
	return spec, family, nil
}

func parseTLSIdentitySpecForFamily(name string, family tlsIdentityInputFamily, data pluginsdk.StateData) (*tlsIdentitySpec, error) {
	switch family {
	case tlsIdentityFamilyPEMFullchain:
		return parseTLSIdentityPEMFullchainSpec(name, data.GetString("fullchain_pem"), data.GetString("private_key_pem"))
	case tlsIdentityFamilyPEMSplit:
		return parseTLSIdentityPEMSplitSpec(name, data.GetString("certificate_pem"), data.GetString("ca_chain_pem"), data.GetString("private_key_pem"))
	case tlsIdentityFamilyDERFullchain:
		return parseTLSIdentityDERFullchainSpec(name, data.GetString("fullchain_der_base64"), data.GetString("private_key_der_base64"))
	case tlsIdentityFamilyDERSplit:
		return parseTLSIdentityDERSplitSpec(name, data.GetString("certificate_der_base64"), data.GetString("ca_chain_der_base64"), data.GetString("private_key_der_base64"))
	default:
		return nil, fmt.Errorf("unsupported tls input family %q", family)
	}
}

func parseTLSIdentityPEMFullchainSpec(name, fullchainPEM, privateKeyPEM string) (*tlsIdentitySpec, error) {
	certs, _, err := normalizePEMCertificates(fullchainPEM, "fullchain_pem")
	if err != nil {
		return nil, err
	}
	privateKey, _, _, err := normalizePrivateKeyPEM(privateKeyPEM)
	if err != nil {
		return nil, err
	}
	return buildTLSIdentitySpec(name, certs, privateKey)
}

func parseTLSIdentityPEMSplitSpec(name, certificatePEM, caChainPEM, privateKeyPEM string) (*tlsIdentitySpec, error) {
	leafCerts, _, err := normalizePEMCertificates(certificatePEM, "certificate_pem")
	if err != nil {
		return nil, err
	}
	if len(leafCerts) != 1 {
		return nil, fmt.Errorf("certificate_pem must contain exactly one PEM CERTIFICATE block")
	}
	chainCerts, _, err := normalizeOptionalPEMCertificates(caChainPEM, "ca_chain_pem")
	if err != nil {
		return nil, err
	}
	privateKey, _, _, err := normalizePrivateKeyPEM(privateKeyPEM)
	if err != nil {
		return nil, err
	}
	return buildTLSIdentitySpec(name, append(leafCerts, chainCerts...), privateKey)
}

func parseTLSIdentityDERFullchainSpec(name, fullchainDERBase64, privateKeyDERBase64 string) (*tlsIdentitySpec, error) {
	certs, _, err := normalizeDERCertificatesBase64(fullchainDERBase64, "fullchain_der_base64")
	if err != nil {
		return nil, err
	}
	privateKey, _, _, err := normalizePrivateKeyDERBase64(privateKeyDERBase64, "private_key_der_base64")
	if err != nil {
		return nil, err
	}
	return buildTLSIdentitySpec(name, certs, privateKey)
}

func parseTLSIdentityDERSplitSpec(name, certificateDERBase64, caChainDERBase64, privateKeyDERBase64 string) (*tlsIdentitySpec, error) {
	leafCerts, _, err := normalizeDERCertificatesBase64(certificateDERBase64, "certificate_der_base64")
	if err != nil {
		return nil, err
	}
	if len(leafCerts) != 1 {
		return nil, fmt.Errorf("certificate_der_base64 must contain exactly one DER certificate")
	}
	chainCerts, _, err := normalizeOptionalDERCertificatesBase64(caChainDERBase64, "ca_chain_der_base64")
	if err != nil {
		return nil, err
	}
	privateKey, _, _, err := normalizePrivateKeyDERBase64(privateKeyDERBase64, "private_key_der_base64")
	if err != nil {
		return nil, err
	}
	return buildTLSIdentitySpec(name, append(leafCerts, chainCerts...), privateKey)
}

func buildTLSIdentitySpec(name string, certs []*x509.Certificate, privateKey interface{}) (*tlsIdentitySpec, error) {
	if len(certs) == 0 {
		return nil, fmt.Errorf("tls identity requires at least one certificate")
	}
	if !privateKeyMatchesCertificate(privateKey, certs[0]) {
		return nil, fmt.Errorf("private key does not match the leaf certificate")
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("marshal private key as PKCS#8: %w", err)
	}

	fullchainPEM := encodeCertificatesPEM(certs)
	privateKeyPEM := encodePrivateKeyPEM(privateKeyDER)

	return &tlsIdentitySpec{
		Name:             name,
		Certificates:     certs,
		FullchainPEM:     fullchainPEM,
		PrivateKeyPEM:    privateKeyPEM,
		PrivateKeyDER:    privateKeyDER,
		FullchainPath:    tlsIdentityFullchainPath(name),
		PrivateKeyPath:   tlsIdentityPrivateKeyPath(name),
		Subject:          certs[0].Subject.String(),
		Issuer:           certs[0].Issuer.String(),
		SerialNumber:     certs[0].SerialNumber.String(),
		NotAfter:         certs[0].NotAfter.UTC().Format(time.RFC3339),
		FullchainDigest:  digestBytes([]byte(fullchainPEM)),
		PrivateKeyDigest: digestBytes([]byte(privateKeyPEM)),
	}, nil
}

func captureTLSIdentitySnapshot(spec *tlsIdentitySpec) (*tlsIdentitySnapshot, error) {
	fullchain, err := captureManagedFile(spec.FullchainPath)
	if err != nil {
		return nil, err
	}
	privateKey, err := captureManagedFile(spec.PrivateKeyPath)
	if err != nil {
		return nil, err
	}
	return &tlsIdentitySnapshot{
		Fullchain:  fullchain,
		PrivateKey: privateKey,
	}, nil
}

func captureManagedFile(path string) (managedFileSnapshot, error) {
	snapshot := managedFileSnapshot{Path: path}
	exists, err := pluginsdk.FileExists(path)
	if err != nil {
		return snapshot, fmt.Errorf("check file %s: %w", path, err)
	}
	if !exists {
		return snapshot, nil
	}

	data, err := pluginsdk.FileRead(path)
	if err != nil {
		return snapshot, fmt.Errorf("read file %s: %w", path, err)
	}
	stat, err := pluginsdk.FileStat_(path)
	if err != nil {
		return snapshot, fmt.Errorf("stat file %s: %w", path, err)
	}

	snapshot.Exists = true
	snapshot.Content = string(data)
	if stat != nil {
		snapshot.Mode = stat.Mode
		snapshot.Owner = stat.Owner
		snapshot.Group = stat.Group
	}
	return snapshot, nil
}

func reconcileTLSIdentity(spec *tlsIdentitySpec, snapshot *tlsIdentitySnapshot) error {
	if err := writeManagedFile(spec.FullchainPath, spec.FullchainPEM, 0o644, snapshot.Fullchain); err != nil {
		return err
	}
	if err := writeManagedFile(spec.PrivateKeyPath, spec.PrivateKeyPEM, 0o600, snapshot.PrivateKey); err != nil {
		return err
	}
	return nil
}

func writeManagedFile(path, content string, mode uint32, snapshot managedFileSnapshot) error {
	if snapshot.Exists && snapshot.Content == content {
		if err := pluginsdk.FileChown(path, "root", "root"); err != nil {
			return fmt.Errorf("chown %s: %w", path, err)
		}
		if err := pluginsdk.FileChmod(path, mode); err != nil {
			return fmt.Errorf("chmod %s: %w", path, err)
		}
		return nil
	}

	if err := pluginsdk.FileWrite(path, []byte(content), mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := pluginsdk.FileChown(path, "root", "root"); err != nil {
		return fmt.Errorf("chown %s: %w", path, err)
	}
	if err := pluginsdk.FileChmod(path, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

func restoreTLSIdentity(snapshot *tlsIdentitySnapshot) error {
	if snapshot == nil {
		return nil
	}

	if err := restoreManagedFile(snapshot.Fullchain); err != nil {
		return err
	}
	if err := restoreManagedFile(snapshot.PrivateKey); err != nil {
		return err
	}
	return nil
}

func restoreManagedFile(snapshot managedFileSnapshot) error {
	if !snapshot.Exists {
		return removePaths(snapshot.Path)
	}

	mode := snapshot.Mode
	if mode == 0 {
		mode = 0o644
	}
	if err := pluginsdk.FileWrite(snapshot.Path, []byte(snapshot.Content), mode); err != nil {
		return fmt.Errorf("restore %s: %w", snapshot.Path, err)
	}
	if snapshot.Owner != "" || snapshot.Group != "" {
		owner := snapshot.Owner
		group := snapshot.Group
		if owner == "" {
			owner = "root"
		}
		if group == "" {
			group = "root"
		}
		if err := pluginsdk.FileChown(snapshot.Path, owner, group); err != nil {
			return fmt.Errorf("restore owner for %s: %w", snapshot.Path, err)
		}
	}
	if err := pluginsdk.FileChmod(snapshot.Path, mode); err != nil {
		return fmt.Errorf("restore mode for %s: %w", snapshot.Path, err)
	}
	return nil
}

func removeTLSIdentity(name string) error {
	return removePaths(tlsIdentityFullchainPath(name), tlsIdentityPrivateKeyPath(name))
}

func removePaths(paths ...string) error {
	for _, path := range paths {
		if err := pluginsdk.FileDelete(path); err != nil {
			return fmt.Errorf("remove managed tls file %s: %w", path, err)
		}
	}
	return nil
}

func tlsIdentityState(spec *tlsIdentitySpec, family tlsIdentityInputFamily) pluginsdk.StateData {
	state := pluginsdk.StateData{
		"id":                 spec.Name,
		"name":               spec.Name,
		"input_family":       string(family),
		"fullchain_path":     spec.FullchainPath,
		"private_key_path":   spec.PrivateKeyPath,
		"subject":            spec.Subject,
		"issuer":             spec.Issuer,
		"serial_number":      spec.SerialNumber,
		"not_after":          spec.NotAfter,
		"fullchain_digest":   spec.FullchainDigest,
		"private_key_digest": spec.PrivateKeyDigest,
	}

	switch family {
	case tlsIdentityFamilyPEMFullchain:
		state["fullchain_pem"] = digestStateValue(spec.FullchainDigest)
		state["private_key_pem"] = digestStateValue(spec.PrivateKeyDigest)
	case tlsIdentityFamilyPEMSplit:
		leafPEM, chainPEM := splitCertificatesPEM(spec.Certificates)
		state["certificate_pem"] = digestStateValue(digestBytes([]byte(leafPEM)))
		if chainPEM != "" {
			state["ca_chain_pem"] = digestStateValue(digestBytes([]byte(chainPEM)))
		}
		state["private_key_pem"] = digestStateValue(spec.PrivateKeyDigest)
	case tlsIdentityFamilyDERFullchain:
		fullchainDER := encodeCertificatesDER(spec.Certificates)
		state["fullchain_der_base64"] = digestStateValue(digestBytes(fullchainDER))
		state["private_key_der_base64"] = digestStateValue(digestBytes(spec.PrivateKeyDER))
	case tlsIdentityFamilyDERSplit:
		leafDER, chainDER := splitCertificatesDER(spec.Certificates)
		state["certificate_der_base64"] = digestStateValue(digestBytes(leafDER))
		if len(chainDER) > 0 {
			state["ca_chain_der_base64"] = digestStateValue(digestBytes(chainDER))
		}
		state["private_key_der_base64"] = digestStateValue(digestBytes(spec.PrivateKeyDER))
	default:
		state["fullchain_pem"] = digestStateValue(spec.FullchainDigest)
		state["private_key_pem"] = digestStateValue(spec.PrivateKeyDigest)
	}

	return state
}

func detectTLSIdentityInputFamily(data pluginsdk.StateData) (tlsIdentityInputFamily, error) {
	has := func(key string) bool {
		return strings.TrimSpace(data.GetString(key)) != ""
	}

	switch {
	case has("fullchain_pem"):
		if has("certificate_pem") || has("ca_chain_pem") || has("fullchain_der_base64") || has("certificate_der_base64") || has("ca_chain_der_base64") || has("private_key_der_base64") {
			return "", fmt.Errorf("fullchain_pem cannot be combined with other certificate input families")
		}
		if !has("private_key_pem") {
			return "", fmt.Errorf("fullchain_pem requires private_key_pem")
		}
		return tlsIdentityFamilyPEMFullchain, nil
	case has("certificate_pem"):
		if has("fullchain_pem") || has("fullchain_der_base64") || has("certificate_der_base64") || has("ca_chain_der_base64") || has("private_key_der_base64") {
			return "", fmt.Errorf("certificate_pem cannot be combined with other certificate input families")
		}
		if !has("private_key_pem") {
			return "", fmt.Errorf("certificate_pem requires private_key_pem")
		}
		return tlsIdentityFamilyPEMSplit, nil
	case has("fullchain_der_base64"):
		if has("fullchain_pem") || has("certificate_pem") || has("ca_chain_pem") || has("certificate_der_base64") || has("ca_chain_der_base64") || has("private_key_pem") {
			return "", fmt.Errorf("fullchain_der_base64 cannot be combined with other certificate input families")
		}
		if !has("private_key_der_base64") {
			return "", fmt.Errorf("fullchain_der_base64 requires private_key_der_base64")
		}
		return tlsIdentityFamilyDERFullchain, nil
	case has("certificate_der_base64"):
		if has("fullchain_pem") || has("certificate_pem") || has("ca_chain_pem") || has("fullchain_der_base64") || has("private_key_pem") {
			return "", fmt.Errorf("certificate_der_base64 cannot be combined with other certificate input families")
		}
		if !has("private_key_der_base64") {
			return "", fmt.Errorf("certificate_der_base64 requires private_key_der_base64")
		}
		return tlsIdentityFamilyDERSplit, nil
	case has("ca_chain_pem"):
		return "", fmt.Errorf("ca_chain_pem requires certificate_pem")
	case has("ca_chain_der_base64"):
		return "", fmt.Errorf("ca_chain_der_base64 requires certificate_der_base64")
	case has("private_key_pem") || has("private_key_der_base64"):
		return "", fmt.Errorf("tls identity requires a certificate input family plus a matching private key input")
	default:
		return "", fmt.Errorf("tls identity requires one of: fullchain_pem + private_key_pem, certificate_pem [+ ca_chain_pem] + private_key_pem, fullchain_der_base64 + private_key_der_base64, or certificate_der_base64 [+ ca_chain_der_base64] + private_key_der_base64")
	}
}

func tlsIdentityFamilyFromState(state pluginsdk.StateData) tlsIdentityInputFamily {
	switch raw := strings.TrimSpace(state.GetString("input_family")); raw {
	case string(tlsIdentityFamilyPEMFullchain):
		return tlsIdentityFamilyPEMFullchain
	case string(tlsIdentityFamilyPEMSplit):
		return tlsIdentityFamilyPEMSplit
	case string(tlsIdentityFamilyDERFullchain):
		return tlsIdentityFamilyDERFullchain
	case string(tlsIdentityFamilyDERSplit):
		return tlsIdentityFamilyDERSplit
	}
	switch {
	case strings.TrimSpace(state.GetString("certificate_der_base64")) != "" || strings.TrimSpace(state.GetString("ca_chain_der_base64")) != "":
		return tlsIdentityFamilyDERSplit
	case strings.TrimSpace(state.GetString("fullchain_der_base64")) != "":
		return tlsIdentityFamilyDERFullchain
	case strings.TrimSpace(state.GetString("certificate_pem")) != "" || strings.TrimSpace(state.GetString("ca_chain_pem")) != "":
		return tlsIdentityFamilyPEMSplit
	default:
		return tlsIdentityFamilyPEMFullchain
	}
}

func normalizePEMCertificates(value, field string) ([]*x509.Certificate, string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, "", fmt.Errorf("%s must not be empty", field)
	}

	var (
		normalizedBlocks []string
		certs            []*x509.Certificate
	)
	rest := []byte(trimmed)
	for len(bytes.TrimSpace(rest)) > 0 {
		block, remaining := pem.Decode(rest)
		if block == nil {
			return nil, "", fmt.Errorf("%s must contain only PEM CERTIFICATE blocks", field)
		}
		if block.Type != "CERTIFICATE" {
			return nil, "", fmt.Errorf("%s must contain only PEM CERTIFICATE blocks", field)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, "", fmt.Errorf("parse certificate in %s: %w", field, err)
		}
		certs = append(certs, cert)
		normalizedBlocks = append(normalizedBlocks, strings.TrimRight(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})), "\n"))
		rest = remaining
	}
	if len(certs) == 0 {
		return nil, "", fmt.Errorf("%s must contain at least one PEM CERTIFICATE block", field)
	}
	return certs, strings.Join(normalizedBlocks, "\n"), nil
}

func normalizeOptionalPEMCertificates(value, field string) ([]*x509.Certificate, string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, "", nil
	}
	return normalizePEMCertificates(value, field)
}

func normalizeDERCertificatesBase64(value, field string) ([]*x509.Certificate, []byte, error) {
	decoded, err := decodeBase64Value(value, field)
	if err != nil {
		return nil, nil, err
	}
	certs, err := x509.ParseCertificates(decoded)
	if err != nil {
		return nil, nil, fmt.Errorf("parse certificates in %s: %w", field, err)
	}
	if len(certs) == 0 {
		return nil, nil, fmt.Errorf("%s must contain at least one DER certificate", field)
	}
	return certs, decoded, nil
}

func normalizeOptionalDERCertificatesBase64(value, field string) ([]*x509.Certificate, []byte, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil, nil
	}
	return normalizeDERCertificatesBase64(value, field)
}

func normalizePrivateKeyPEM(value string) (interface{}, []byte, string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, nil, "", fmt.Errorf("private_key_pem must not be empty")
	}
	block, rest := pem.Decode([]byte(trimmed))
	if block == nil {
		return nil, nil, "", fmt.Errorf("private_key_pem must contain a PEM private key block")
	}
	if len(bytes.TrimSpace(rest)) != 0 {
		return nil, nil, "", fmt.Errorf("private_key_pem must contain exactly one PEM private key block")
	}
	privateKey, err := parsePrivateKey(block)
	if err != nil {
		return nil, nil, "", err
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, nil, "", fmt.Errorf("marshal PEM private key as PKCS#8: %w", err)
	}
	return privateKey, privateKeyDER, encodePrivateKeyPEM(privateKeyDER), nil
}

func normalizePrivateKeyDERBase64(value, field string) (interface{}, []byte, string, error) {
	decoded, err := decodeBase64Value(value, field)
	if err != nil {
		return nil, nil, "", err
	}
	privateKey, err := parsePrivateKeyDER(decoded)
	if err != nil {
		return nil, nil, "", fmt.Errorf("parse %s: %w", field, err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, nil, "", fmt.Errorf("marshal %s as PKCS#8: %w", field, err)
	}
	return privateKey, privateKeyDER, encodePrivateKeyPEM(privateKeyDER), nil
}

func decodeBase64Value(value, field string) ([]byte, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, fmt.Errorf("%s must not be empty", field)
	}
	compact := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\n', '\r', '\t':
			return -1
		default:
			return r
		}
	}, trimmed)
	decoded, err := base64.StdEncoding.DecodeString(compact)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", field, err)
	}
	return decoded, nil
}

func parsePrivateKey(block *pem.Block) (interface{}, error) {
	key, err := parsePrivateKeyDER(block.Bytes)
	if err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("unsupported private key PEM block type %q", block.Type)
}

func parsePrivateKeyDER(der []byte) (interface{}, error) {
	if key, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		return key, nil
	}
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}
	if key, err := x509.ParseECPrivateKey(der); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("unsupported DER private key format")
}

func privateKeyMatchesCertificate(privateKey interface{}, cert *x509.Certificate) bool {
	switch pub := cert.PublicKey.(type) {
	case *rsa.PublicKey:
		key, ok := privateKey.(*rsa.PrivateKey)
		return ok && pub.N.Cmp(key.PublicKey.N) == 0 && pub.E == key.PublicKey.E
	case *ecdsa.PublicKey:
		key, ok := privateKey.(*ecdsa.PrivateKey)
		return ok && pub.X.Cmp(key.PublicKey.X) == 0 && pub.Y.Cmp(key.PublicKey.Y) == 0
	case ed25519.PublicKey:
		key, ok := privateKey.(ed25519.PrivateKey)
		if !ok {
			return false
		}
		derived, ok := key.Public().(ed25519.PublicKey)
		return ok && bytes.Equal(pub, derived)
	default:
		return false
	}
}

func tlsIdentityNameFromState(state pluginsdk.StateData) (string, error) {
	if name, err := normalizeTLSIdentityName(state.GetString("name")); err == nil {
		return name, nil
	}
	if name, err := normalizeTLSIdentityName(state.GetString("id")); err == nil {
		return name, nil
	}
	if path := strings.TrimSpace(state.GetString("fullchain_path")); path != "" {
		return tlsIdentityNameFromImportID(path)
	}
	if path := strings.TrimSpace(state.GetString("private_key_path")); path != "" {
		return tlsIdentityNameFromImportID(path)
	}
	return "", fmt.Errorf("tls identity requires name, id, fullchain_path, or private_key_path")
}

func tlsIdentityNameFromImportID(id string) (string, error) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return "", fmt.Errorf("import ID must be a TLS identity name or managed path")
	}
	if strings.HasPrefix(trimmed, "/") {
		return tlsIdentityNameFromManagedPath(trimmed)
	}
	return normalizeTLSIdentityName(trimmed)
}

func tlsIdentityNameFromManagedPath(path string) (string, error) {
	cleaned := filepath.Clean(path)
	switch filepath.Dir(cleaned) {
	case tlsIdentityFullchainDir:
		base := filepath.Base(cleaned)
		if !strings.HasSuffix(base, "-fullchain.pem") {
			return "", fmt.Errorf("fullchain path %q must end with -fullchain.pem", path)
		}
		return normalizeTLSIdentityName(strings.TrimSuffix(base, "-fullchain.pem"))
	case tlsIdentityPrivateDir:
		base := filepath.Base(cleaned)
		if !strings.HasSuffix(base, ".key") {
			return "", fmt.Errorf("private key path %q must end with .key", path)
		}
		return normalizeTLSIdentityName(strings.TrimSuffix(base, ".key"))
	default:
		return "", fmt.Errorf("import path %q is not a managed tls identity path", path)
	}
}

func normalizeTLSIdentityName(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("name must not be empty")
	}
	if trimmed == "." || trimmed == ".." {
		return "", fmt.Errorf("name must not be %q", trimmed)
	}
	if trimmed != filepath.Base(trimmed) {
		return "", fmt.Errorf("name must not be path-like")
	}
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.', r == '-', r == '_':
		default:
			return "", fmt.Errorf("name may contain only letters, digits, dots, dashes, and underscores")
		}
	}
	return trimmed, nil
}

func encodeCertificatesPEM(certs []*x509.Certificate) string {
	blocks := make([]string, 0, len(certs))
	for _, cert := range certs {
		blocks = append(blocks, strings.TrimRight(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})), "\n"))
	}
	return strings.Join(blocks, "\n")
}

func splitCertificatesPEM(certs []*x509.Certificate) (string, string) {
	if len(certs) == 0 {
		return "", ""
	}
	if len(certs) == 1 {
		return encodeCertificatesPEM(certs[:1]), ""
	}
	return encodeCertificatesPEM(certs[:1]), encodeCertificatesPEM(certs[1:])
}

func encodeCertificatesDER(certs []*x509.Certificate) []byte {
	var out []byte
	for _, cert := range certs {
		out = append(out, cert.Raw...)
	}
	return out
}

func splitCertificatesDER(certs []*x509.Certificate) ([]byte, []byte) {
	if len(certs) == 0 {
		return nil, nil
	}
	if len(certs) == 1 {
		return certs[0].Raw, nil
	}
	return certs[0].Raw, encodeCertificatesDER(certs[1:])
}

func encodePrivateKeyPEM(pkcs8DER []byte) string {
	return strings.TrimRight(string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8DER})), "\n")
}

func tlsIdentityFullchainPath(name string) string {
	return filepath.Join(tlsIdentityFullchainDir, name+"-fullchain.pem")
}

func tlsIdentityPrivateKeyPath(name string) string {
	return filepath.Join(tlsIdentityPrivateDir, name+".key")
}

func ensureLinuxTLSIdentity() error {
	profile, err := pluginsdk.GetHostProfile()
	if err != nil {
		return fmt.Errorf("detect host profile: %w", err)
	}
	if profile == nil {
		return fmt.Errorf("host profile unavailable")
	}
	return nil
}

func digestBytes(data []byte) string {
	return digestutil.MustDigestBytes(digestutil.AlgorithmBlake3, data)
}

func digestStateValue(digest string) string {
	return digest
}

func init() {
	pluginsdk.RegisterResource(&tlsIdentityResource{})
}

func main() {
	pluginsdk.Run()
}
