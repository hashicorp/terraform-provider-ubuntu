// Copyright IBM Corp. 2026

package transport

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"

	digestutil "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/digest"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type HostKeyTrustStore struct {
	mu             sync.RWMutex
	exact          map[string]map[string]struct{}
	knownHostFiles map[string]knownHostFileEntries
}

type knownHostFileEntries struct {
	digest   string
	exact    map[string]map[string]struct{}
	patterns []hostKeyPatternEntry
	revoked  map[string]struct{}
}

type hostKeyPatternEntry struct {
	matcher     hostAddressMatcher
	fingerprint string
}

type hostAddressMatcher interface {
	match(knownHostAddress) bool
}

type knownHostAddress struct {
	host string
	port string
}

func (a knownHostAddress) String() string {
	host := a.host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return host + ":" + a.port
}

type knownHostPattern struct {
	negate bool
	addr   knownHostAddress
}

type knownHostPatterns []knownHostPattern

type hashedKnownHost struct {
	salt []byte
	mac  []byte
}

const openSSHHashedHostType = "1"

func NewHostKeyTrustStore() *HostKeyTrustStore {
	return &HostKeyTrustStore{
		exact:          make(map[string]map[string]struct{}),
		knownHostFiles: make(map[string]knownHostFileEntries),
	}
}

func (s *HostKeyTrustStore) LoadKnownHostsFile(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("open known_hosts %s: %w", path, err)
	}

	digest := digestutil.MustDigestBytes(digestutil.AlgorithmBlake3, contents)

	s.mu.RLock()
	loaded := s.knownHostFiles[path]
	s.mu.RUnlock()
	if loaded.digest == digest {
		return nil
	}

	entries, err := parseKnownHosts(contents, path)
	if err != nil {
		return err
	}
	entries.digest = digest

	s.mu.Lock()
	s.knownHostFiles[path] = entries
	s.mu.Unlock()

	return nil
}

func parseKnownHosts(contents []byte, source string) (knownHostFileEntries, error) {
	entries := knownHostFileEntries{
		exact:   make(map[string]map[string]struct{}),
		revoked: make(map[string]struct{}),
	}
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	lineNumber := 0

	for scanner.Scan() {
		lineNumber++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || line[0] == '#' {
			continue
		}

		marker, hosts, pubKey, _, _, err := ssh.ParseKnownHosts(line)
		if err != nil {
			return knownHostFileEntries{}, fmt.Errorf("parse known_hosts %s:%d: %w", source, lineNumber, err)
		}

		fingerprint := hostKeyFingerprint(pubKey)

		switch marker {
		case "":
			for _, host := range hosts {
				host = strings.TrimSpace(host)
				if host == "" {
					continue
				}
				if isExactKnownHostPattern(host) {
					trustFingerprintInMap(entries.exact, host, fingerprint)
					continue
				}

				matcher, matchErr := newKnownHostMatcher(host)
				if matchErr != nil {
					return knownHostFileEntries{}, fmt.Errorf("parse known_hosts %s:%d host pattern %q: %w", source, lineNumber, host, matchErr)
				}
				entries.patterns = append(entries.patterns, hostKeyPatternEntry{matcher: matcher, fingerprint: fingerprint})
			}
		case "@revoked":
			entries.revoked[fingerprint] = struct{}{}
		default:
			return knownHostFileEntries{}, fmt.Errorf("parse known_hosts %s:%d: unsupported marker %q", source, lineNumber, marker)
		}
	}

	if err := scanner.Err(); err != nil {
		return knownHostFileEntries{}, fmt.Errorf("read known_hosts %s: %w", source, err)
	}

	return entries, nil
}

func hostKeyFingerprint(key ssh.PublicKey) string {
	if key == nil {
		return ""
	}
	return digestutil.MustDigestBytes(digestutil.AlgorithmBlake3, key.Marshal())
}

func (s *HostKeyTrustStore) TrustFingerprint(address, fingerprint string) {
	address = normalizeKnownHostAddress(address)
	fingerprint = strings.TrimSpace(fingerprint)
	if address == "" || fingerprint == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.trustFingerprintLocked(address, fingerprint)
}

func (s *HostKeyTrustStore) TrustFingerprintAliases(addresses []string, fingerprint string) {
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, address := range addresses {
		address = normalizeKnownHostAddress(address)
		if address == "" {
			continue
		}
		s.trustFingerprintLocked(address, fingerprint)
	}
}

func (s *HostKeyTrustStore) TrustedFingerprints(addresses []string) map[string]struct{} {
	normalized := normalizeKnownHostAddresses(addresses)
	parsed := make([]knownHostAddress, 0, len(normalized))
	for _, address := range normalized {
		parsedAddress, ok := parseKnownHostAddress(address)
		if ok {
			parsed = append(parsed, parsedAddress)
		}
	}

	trusted := make(map[string]struct{})

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, address := range normalized {
		for fingerprint := range s.exact[address] {
			if !s.isRevokedFingerprintLocked(fingerprint) {
				trusted[fingerprint] = struct{}{}
			}
		}
		for _, file := range s.knownHostFiles {
			for fingerprint := range file.exact[address] {
				if !s.isRevokedFingerprintLocked(fingerprint) {
					trusted[fingerprint] = struct{}{}
				}
			}
		}
	}

	for _, file := range s.knownHostFiles {
		for _, entry := range file.patterns {
			for _, address := range parsed {
				if entry.matcher.match(address) {
					if !s.isRevokedFingerprintLocked(entry.fingerprint) {
						trusted[entry.fingerprint] = struct{}{}
					}
					break
				}
			}
		}
	}

	return trusted
}

func (s *HostKeyTrustStore) IsRevokedFingerprint(fingerprint string) bool {
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isRevokedFingerprintLocked(fingerprint)
}

func (s *HostKeyTrustStore) trustFingerprintLocked(address, fingerprint string) {
	if s.isRevokedFingerprintLocked(fingerprint) {
		return
	}
	trustFingerprintInMap(s.exact, address, fingerprint)
}

func (s *HostKeyTrustStore) isRevokedFingerprintLocked(fingerprint string) bool {
	for _, file := range s.knownHostFiles {
		if _, revoked := file.revoked[fingerprint]; revoked {
			return true
		}
	}
	return false
}

func trustFingerprintInMap(exact map[string]map[string]struct{}, address, fingerprint string) {
	address = normalizeKnownHostAddress(address)
	fingerprint = strings.TrimSpace(fingerprint)
	if address == "" || fingerprint == "" {
		return
	}
	if exact[address] == nil {
		exact[address] = make(map[string]struct{})
	}
	exact[address][fingerprint] = struct{}{}
}

func normalizeKnownHostAddresses(addresses []string) []string {
	seen := make(map[string]struct{}, len(addresses))
	normalized := make([]string, 0, len(addresses))
	for _, address := range addresses {
		address = normalizeKnownHostAddress(address)
		if address == "" {
			continue
		}
		if _, ok := seen[address]; ok {
			continue
		}
		seen[address] = struct{}{}
		normalized = append(normalized, address)
	}
	return normalized
}

func normalizeKnownHostAddress(address string) string {
	address = strings.TrimSpace(address)
	if address == "" {
		return ""
	}
	return knownhosts.Normalize(address)
}

func isExactKnownHostPattern(pattern string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || strings.HasPrefix(pattern, "|") || strings.HasPrefix(pattern, "!") {
		return false
	}
	return !strings.ContainsAny(pattern, "*?")
}

func parseKnownHostAddress(address string) (knownHostAddress, bool) {
	address = normalizeKnownHostAddress(address)
	if address == "" {
		return knownHostAddress{}, false
	}

	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return knownHostAddress{host: address, port: "22"}, true
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	return knownHostAddress{host: host, port: port}, true
}

func newKnownHostMatcher(pattern string) (hostAddressMatcher, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, fmt.Errorf("missing host pattern")
	}
	if strings.HasPrefix(pattern, "|") {
		return newHashedKnownHost(pattern)
	}
	return newKnownHostPatterns(pattern)
}

func newKnownHostPatterns(pattern string) (knownHostPatterns, error) {
	parts := strings.Split(pattern, ",")
	patterns := make(knownHostPatterns, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		negate := false
		if strings.HasPrefix(part, "!") {
			negate = true
			part = strings.TrimPrefix(part, "!")
		}
		if part == "" {
			return nil, fmt.Errorf("negation without hostname")
		}

		address, ok := parseKnownHostAddress(part)
		if !ok {
			return nil, fmt.Errorf("invalid host pattern %q", part)
		}
		patterns = append(patterns, knownHostPattern{negate: negate, addr: address})
	}
	return patterns, nil
}

func (p knownHostPatterns) match(address knownHostAddress) bool {
	matched := false
	for _, pattern := range p {
		if !pattern.match(address) {
			continue
		}
		if pattern.negate {
			return false
		}
		matched = true
	}
	return matched
}

func (p knownHostPattern) match(address knownHostAddress) bool {
	return wildcardMatch([]byte(p.addr.host), []byte(address.host)) && p.addr.port == address.port
}

func wildcardMatch(pattern []byte, value []byte) bool {
	for {
		if len(pattern) == 0 {
			return len(value) == 0
		}
		if len(value) == 0 {
			return false
		}

		switch pattern[0] {
		case '*':
			if len(pattern) == 1 {
				return true
			}
			for index := range value {
				if wildcardMatch(pattern[1:], value[index:]) {
					return true
				}
			}
			return false
		case '?':
			pattern = pattern[1:]
			value = value[1:]
		default:
			if pattern[0] != value[0] {
				return false
			}
			pattern = pattern[1:]
			value = value[1:]
		}
	}
}

func newHashedKnownHost(encoded string) (*hashedKnownHost, error) {
	components := strings.Split(encoded, "|")
	if len(components) != 4 || components[0] != "" {
		return nil, fmt.Errorf("invalid hashed host pattern %q", encoded)
	}
	if components[1] != openSSHHashedHostType {
		return nil, fmt.Errorf("hashed host pattern %q uses unsupported OpenSSH hashed host type %q", encoded, components[1])
	}

	salt, err := base64.StdEncoding.DecodeString(components[2])
	if err != nil {
		return nil, fmt.Errorf("decode hashed host salt: %w", err)
	}
	mac, err := base64.StdEncoding.DecodeString(components[3])
	if err != nil {
		return nil, fmt.Errorf("decode hashed host MAC: %w", err)
	}

	return &hashedKnownHost{salt: salt, mac: mac}, nil
}

func (h *hashedKnownHost) match(address knownHostAddress) bool {
	return bytes.Equal(openSSHKnownHostMAC(normalizeKnownHostAddress(address.String()), h.salt), h.mac)
}

func openSSHKnownHostMAC(hostname string, salt []byte) []byte {
	mac := hmac.New(sha1.New, salt)
	_, _ = mac.Write([]byte(hostname))
	return mac.Sum(nil)
}
