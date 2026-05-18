package truststore

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
	digestutil "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/digest"
	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/hostfs"
)

type Options struct {
	Directory             string
	EnsureTrailingNewline bool
	NonCAError            string
	MissingPathError      string
	AcceptRelativeID      bool
	RefreshAfterRestore   bool
}

type Spec struct {
	Name        string
	Certificate string
	Path        string
	Subject     string
	Issuer      string
	Digest      string
}

func DesiredSpec(data pluginsdk.StateData, opts Options) (*Spec, error) {
	name := SanitizeName(data.GetString("name"))
	if name == "" {
		return nil, fmt.Errorf("name must not be empty")
	}
	certificate, subject, issuer, err := NormalizeCertificate(data.GetString("certificate"), opts)
	if err != nil {
		return nil, err
	}
	return &Spec{
		Name:        name,
		Certificate: certificate,
		Path:        filepath.Join(opts.Directory, name+".crt"),
		Subject:     subject,
		Issuer:      issuer,
		Digest:      digestBytes([]byte(certificate)),
	}, nil
}

func ParseSpec(name, certificate, path string, opts Options) (*Spec, error) {
	normalized, subject, issuer, err := NormalizeCertificate(certificate, opts)
	if err != nil {
		return nil, err
	}
	name = SanitizeName(name)
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), ".crt")
	}
	return &Spec{
		Name:        name,
		Certificate: normalized,
		Path:        path,
		Subject:     subject,
		Issuer:      issuer,
		Digest:      digestBytes([]byte(normalized)),
	}, nil
}

func NormalizeCertificate(content string, opts Options) (string, string, string, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "", "", "", fmt.Errorf("certificate must not be empty")
	}

	block, rest := pem.Decode([]byte(trimmed))
	if block == nil || block.Type != "CERTIFICATE" {
		return "", "", "", fmt.Errorf("certificate must contain a PEM CERTIFICATE block")
	}
	if len(bytes.TrimSpace(rest)) != 0 {
		return "", "", "", fmt.Errorf("certificate must contain exactly one PEM CERTIFICATE block")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", "", "", fmt.Errorf("parse certificate: %w", err)
	}
	if !cert.IsCA {
		return "", "", "", errors.New(opts.NonCAError)
	}

	normalized := strings.TrimRight(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})), "\n")
	if opts.EnsureTrailingNewline {
		normalized += "\n"
	}
	return normalized, cert.Subject.String(), cert.Issuer.String(), nil
}

func Path(data pluginsdk.StateData, opts Options) (string, error) {
	if path := strings.TrimSpace(data.GetString("cert_path")); path != "" {
		return path, nil
	}
	id := strings.TrimSpace(data.GetString("id"))
	if id != "" && (opts.AcceptRelativeID || strings.HasPrefix(id, "/")) {
		return id, nil
	}
	name := SanitizeName(data.GetString("name"))
	if name == "" {
		return "", errors.New(opts.MissingPathError)
	}
	return filepath.Join(opts.Directory, name+".crt"), nil
}

func ImportPath(id string, opts Options) (string, error) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return "", fmt.Errorf("import ID must be a trusted certificate path or basename")
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = filepath.Join(opts.Directory, SanitizeName(trimmed)+".crt")
	}
	return trimmed, nil
}

func State(spec *Spec) pluginsdk.StateData {
	return pluginsdk.StateData{
		"id":          spec.Path,
		"name":        spec.Name,
		"certificate": spec.Certificate,
		"cert_path":   spec.Path,
		"subject":     spec.Subject,
		"issuer":      spec.Issuer,
		"digest":      spec.Digest,
	}
}

func ApplySpec(spec *Spec, opts Options, refresh func() error) (pluginsdk.StateData, error) {
	snapshot, err := hostfs.CaptureSnapshot(spec.Path)
	if err != nil {
		return nil, fmt.Errorf("capture trusted cert %s: %w", spec.Path, err)
	}

	existing, err := pluginsdk.FileRead(spec.Path)
	changed := true
	if err == nil && string(existing) == spec.Certificate {
		changed = false
	} else if err != nil {
		exists, existsErr := pluginsdk.FileExists(spec.Path)
		if existsErr != nil {
			return nil, fmt.Errorf("check trusted cert %s: %w", spec.Path, existsErr)
		}
		if exists {
			return nil, fmt.Errorf("read trusted cert %s: %w", spec.Path, err)
		}
	}

	if changed {
		if err := pluginsdk.FileWrite(spec.Path, []byte(spec.Certificate), 0o644); err != nil {
			return nil, fmt.Errorf("write trusted cert %s: %w", spec.Path, err)
		}
		if err := refresh(); err != nil {
			if restoreErr := restoreSnapshot(snapshot, opts, refresh); restoreErr != nil {
				return nil, fmt.Errorf("%w (restore failed: %v)", err, restoreErr)
			}
			return nil, err
		}
	}

	return State(spec), nil
}

func DeletePath(path string, opts Options, refresh func() error) error {
	snapshot, err := hostfs.CaptureSnapshot(path)
	if err != nil {
		return fmt.Errorf("capture trusted cert %s: %w", path, err)
	}
	changed, err := hostfs.RemoveIfExists(path)
	if err != nil {
		return fmt.Errorf("remove trusted cert %s: %w", path, err)
	}
	if !changed {
		return nil
	}
	if err := refresh(); err != nil {
		if restoreErr := restoreSnapshot(snapshot, opts, refresh); restoreErr != nil {
			return fmt.Errorf("%w (restore failed: %v)", err, restoreErr)
		}
		return err
	}
	return nil
}

func SanitizeName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimSuffix(value, ".crt")
	if value == "" {
		return ""
	}
	replacer := strings.NewReplacer("/", "-", " ", "-", "_", "-", ".", "-")
	value = replacer.Replace(value)
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "-")
}

func restoreSnapshot(snapshot *hostfs.FileSnapshot, opts Options, refresh func() error) error {
	if err := hostfs.RestoreSnapshot(snapshot, 0o644); err != nil {
		return err
	}
	if opts.RefreshAfterRestore {
		if err := refresh(); err != nil {
			return err
		}
	}
	return nil
}

func digestBytes(data []byte) string {
	return digestutil.MustDigestBytes(digestutil.AlgorithmBlake3, data)
}
