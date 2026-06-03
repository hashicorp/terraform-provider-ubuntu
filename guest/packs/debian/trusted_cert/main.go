// Copyright IBM Corp. 2026

package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
	trustedcertcontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/trustedcert"
	truststoresdk "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/truststore"
)

const trustedCertDir = "/usr/local/share/ca-certificates"

type trustedCertResource struct{}

type trustedCertSpec = truststoresdk.Spec

var trustedCertOptions = truststoresdk.Options{
	Directory:             trustedCertDir,
	EnsureTrailingNewline: false,
	NonCAError:            "trusted certificate must be a CA certificate",
	MissingPathError:      "trusted cert path requires name, cert_path, or id",
	AcceptRelativeID:      true,
	RefreshAfterRestore:   false,
}

func (r *trustedCertResource) Name() string { return "trusted_cert" }

func (r *trustedCertResource) Schema() pluginsdk.Schema {
	return trustedcertcontract.ResourceSchema()
}

func (r *trustedCertResource) Validate(config pluginsdk.StateData) error {
	if err := ensureDebianTrustStore(); err != nil {
		return err
	}
	if _, ok := config["certificate"]; !ok {
		return nil
	}
	_, err := desiredTrustedCertSpec(config)
	return err
}

func (r *trustedCertResource) Read(state pluginsdk.StateData) (pluginsdk.StateData, error) {
	if err := ensureDebianTrustStore(); err != nil {
		return nil, err
	}

	path, err := trustedCertPath(state)
	if err != nil {
		return nil, err
	}

	exists, err := pluginsdk.FileExists(path)
	if err != nil {
		return nil, fmt.Errorf("check trusted cert %s: %w", path, err)
	}
	if !exists {
		return nil, nil
	}

	data, err := pluginsdk.FileRead(path)
	if err != nil {
		return nil, fmt.Errorf("read trusted cert %s: %w", path, err)
	}

	spec, err := parseTrustedCertSpec(state.GetString("name"), string(data), path)
	if err != nil {
		return nil, err
	}

	return trustedCertState(spec), nil
}

func (r *trustedCertResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	if err := ensureDebianTrustStore(); err != nil {
		return nil, err
	}

	spec, err := desiredTrustedCertSpec(plan)
	if err != nil {
		return nil, err
	}

	exists, err := pluginsdk.FileExists(spec.Path)
	if err != nil {
		return nil, fmt.Errorf("check trusted cert %s: %w", spec.Path, err)
	}
	if exists {
		return nil, fmt.Errorf("trusted certificate %q already exists at %s; import it before managing with terraform", spec.Name, spec.Path)
	}

	return applyTrustedCert(plan)
}

func (r *trustedCertResource) Update(_ pluginsdk.StateData, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applyTrustedCert(plan)
}

func (r *trustedCertResource) Delete(state pluginsdk.StateData) error {
	if err := ensureDebianTrustStore(); err != nil {
		return err
	}
	path, err := trustedCertPath(state)
	if err != nil {
		return err
	}
	return truststoresdk.DeletePath(path, trustedCertOptions, updateTrustStore)
}

func (r *trustedCertResource) ImportState(id string) (pluginsdk.StateData, error) {
	if err := ensureDebianTrustStore(); err != nil {
		return nil, err
	}
	trimmed, err := truststoresdk.ImportPath(id, trustedCertOptions)
	if err != nil {
		return nil, err
	}
	data, err := pluginsdk.FileRead(trimmed)
	if err != nil {
		return nil, fmt.Errorf("read trusted cert %s: %w", trimmed, err)
	}
	name := strings.TrimSuffix(filepath.Base(trimmed), ".crt")
	spec, err := parseTrustedCertSpec(name, string(data), trimmed)
	if err != nil {
		return nil, err
	}
	return trustedCertState(spec), nil
}

func applyTrustedCert(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	if err := ensureDebianTrustStore(); err != nil {
		return nil, err
	}

	spec, err := desiredTrustedCertSpec(plan)
	if err != nil {
		return nil, err
	}
	return truststoresdk.ApplySpec(spec, trustedCertOptions, updateTrustStore)
}

func desiredTrustedCertSpec(data pluginsdk.StateData) (*trustedCertSpec, error) {
	return truststoresdk.DesiredSpec(data, trustedCertOptions)
}

func parseTrustedCertSpec(name, certificate, path string) (*trustedCertSpec, error) {
	return truststoresdk.ParseSpec(name, certificate, path, trustedCertOptions)
}

func normalizeTrustedCertificate(content string) (string, string, string, error) {
	return truststoresdk.NormalizeCertificate(content, trustedCertOptions)
}

func trustedCertPath(data pluginsdk.StateData) (string, error) {
	return truststoresdk.Path(data, trustedCertOptions)
}

func sanitizeTrustedCertName(value string) string {
	return truststoresdk.SanitizeName(value)
}

func trustedCertState(spec *trustedCertSpec) pluginsdk.StateData {
	return truststoresdk.State(spec)
}

func updateTrustStore() error {
	res, err := pluginsdk.CmdExec("update-ca-certificates", []string{})
	if err != nil {
		return fmt.Errorf("update-ca-certificates: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("update-ca-certificates failed (exit %d): %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func ensureDebianTrustStore() error {
	profile, err := pluginsdk.LoadHostProfile()
	if err != nil {
		return fmt.Errorf("detect host profile: %w", err)
	}
	if profile.DistroFamily != "debian" {
		return fmt.Errorf("trusted_cert requires a Debian-family host, got distro family %q", profile.DistroFamily)
	}
	if !profile.HasCommand("update-ca-certificates") {
		return fmt.Errorf("trusted_cert requires update-ca-certificates")
	}
	return nil
}

func init() {
	pluginsdk.RegisterResource(&trustedCertResource{})
}

func main() {
	pluginsdk.Run()
}
