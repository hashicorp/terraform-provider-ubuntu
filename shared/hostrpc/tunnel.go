package hostrpc

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"runtime"
	"sync"

	"github.com/cloudflare/circl/kem"
	"github.com/cloudflare/circl/kem/hybrid"
	"github.com/creachadair/jrpc2/channel"
	"github.com/zeebo/blake3"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/sys/cpu"
)

const (
	tunnelProtocolMagic               = "TFN-TUNNEL"
	tunnelProtocolV2                  = 2
	tunnelSuiteHybridAES256GCM        = "X25519-MLKEM768-AES256-GCM-BLAKE3"
	tunnelSuiteHybridChaCha20Poly1305 = "X25519-MLKEM768-CHACHA20-POLY1305-BLAKE3"
)

var tunnelRecordMagic = []byte{'T', 'F', 'N', 'E'}

var (
	tunnelClientSupportedSuites = supportedTunnelSuites
	tunnelServerSupportedSuites = supportedTunnelSuites
)

type ChannelOptions struct {
	EncryptedTunnel      bool
	CompressionThreshold int
}

type tunnelMessage struct {
	Magic      string   `json:"magic"`
	Version    int      `json:"version,omitempty"`
	Type       string   `json:"type"`
	Suites     []string `json:"suites,omitempty"`
	Suite      string   `json:"suite,omitempty"`
	Nonce      []byte   `json:"nonce,omitempty"`
	PublicKey  []byte   `json:"public_key,omitempty"`
	Ciphertext []byte   `json:"ciphertext,omitempty"`
	VerifyData []byte   `json:"verify_data,omitempty"`
}

type tunnelSecrets struct {
	clientKey        []byte
	serverKey        []byte
	clientNonceSeed  []byte
	serverNonceSeed  []byte
	clientFinishKey  []byte
	serverFinishKey  []byte
	transcriptDigest []byte
}

type secureChannel struct {
	base channel.Channel

	sendAEAD      cipher.AEAD
	recvAEAD      cipher.AEAD
	sendNonceSeed [12]byte
	recvNonceSeed [12]byte

	sendMu  sync.Mutex
	recvMu  sync.Mutex
	sendSeq uint64
	recvSeq uint64
}

func NewClientChannel(reader io.Reader, writer io.WriteCloser, opts ChannelOptions) (channel.Channel, error) {
	base := channel.LSP(reader, writer)
	if opts.EncryptedTunnel {
		secured, err := handshakeClient(base)
		if err != nil {
			_ = base.Close()
			return nil, err
		}
		base = secured
	}
	return NewCompressedChannel(base, opts.CompressionThreshold), nil
}

func NewServerChannel(reader io.Reader, writer io.WriteCloser, opts ChannelOptions) (channel.Channel, error) {
	base := channel.LSP(reader, writer)
	if opts.EncryptedTunnel {
		secured, err := handshakeServer(base)
		if err != nil {
			_ = base.Close()
			return nil, err
		}
		base = secured
	}
	return NewCompressedChannel(base, opts.CompressionThreshold), nil
}

func handshakeClient(base channel.Channel) (channel.Channel, error) {
	clientNonce, err := randomBytes(32)
	if err != nil {
		return nil, fmt.Errorf("generate tunnel client nonce: %w", err)
	}

	transcript := blake3.New()
	supportedSuites := tunnelClientSupportedSuites()
	clientHello := tunnelMessage{
		Magic:   tunnelProtocolMagic,
		Version: tunnelProtocolV2,
		Type:    "client_hello",
		Suites:  supportedSuites,
		Nonce:   clientNonce,
	}
	if err := sendHandshakeMessage(base, transcript, clientHello); err != nil {
		return nil, err
	}

	serverHello, serverHelloRaw, err := recvHandshakeMessage(base, "server_hello")
	if err != nil {
		return nil, err
	}
	if serverHello.Version != tunnelProtocolV2 {
		return nil, fmt.Errorf("unsupported executor tunnel protocol version %d", serverHello.Version)
	}
	if !containsString(supportedSuites, serverHello.Suite) {
		return nil, fmt.Errorf("unsupported executor tunnel suite %q", serverHello.Suite)
	}
	if len(serverHello.PublicKey) == 0 {
		return nil, errors.New("executor tunnel hello omitted public key")
	}
	appendTranscript(transcript, serverHelloRaw)

	scheme := hybrid.X25519MLKEM768()
	pub, err := scheme.UnmarshalBinaryPublicKey(serverHello.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("decode executor tunnel public key: %w", err)
	}
	ciphertext, sharedSecret, err := scheme.Encapsulate(pub)
	if err != nil {
		return nil, fmt.Errorf("encapsulate executor tunnel key: %w", err)
	}

	clientKey := tunnelMessage{
		Magic:      tunnelProtocolMagic,
		Version:    tunnelProtocolV2,
		Type:       "client_key",
		Ciphertext: ciphertext,
	}
	if err := sendHandshakeMessage(base, transcript, clientKey); err != nil {
		return nil, err
	}

	secrets, err := deriveTunnelSecrets(sharedSecret, transcript.Sum(nil))
	if err != nil {
		return nil, err
	}

	serverFinish, _, err := recvHandshakeMessage(base, "server_finish")
	if err != nil {
		return nil, err
	}
	if err := verifyFinish(serverFinish.VerifyData, secrets.serverFinishKey, secrets.transcriptDigest); err != nil {
		return nil, fmt.Errorf("verify executor tunnel finish: %w", err)
	}

	clientFinish := tunnelMessage{
		Magic:      tunnelProtocolMagic,
		Version:    tunnelProtocolV2,
		Type:       "client_finish",
		VerifyData: finishDigest(secrets.clientFinishKey, secrets.transcriptDigest),
	}
	if err := sendHandshakeMessage(base, nil, clientFinish); err != nil {
		return nil, err
	}

	return newSecureChannel(base, serverHello.Suite, secrets.clientKey, secrets.serverKey, secrets.clientNonceSeed, secrets.serverNonceSeed)
}

func handshakeServer(base channel.Channel) (channel.Channel, error) {
	clientHello, clientHelloRaw, err := recvHandshakeMessage(base, "client_hello")
	if err != nil {
		return nil, err
	}
	if clientHello.Version != tunnelProtocolV2 {
		return nil, fmt.Errorf("unsupported provider tunnel protocol version %d", clientHello.Version)
	}
	suite, err := selectTunnelSuite(clientHello.Suites, tunnelServerSupportedSuites())
	if err != nil {
		return nil, err
	}

	serverNonce, err := randomBytes(32)
	if err != nil {
		return nil, fmt.Errorf("generate tunnel executor nonce: %w", err)
	}
	scheme := hybrid.X25519MLKEM768()
	pub, priv, err := scheme.GenerateKeyPair()
	if err != nil {
		return nil, fmt.Errorf("generate executor tunnel key pair: %w", err)
	}
	pubBytes, err := pub.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("marshal executor tunnel public key: %w", err)
	}

	transcript := blake3.New()
	appendTranscript(transcript, clientHelloRaw)
	serverHello := tunnelMessage{
		Magic:     tunnelProtocolMagic,
		Version:   tunnelProtocolV2,
		Type:      "server_hello",
		Suite:     suite,
		Nonce:     serverNonce,
		PublicKey: pubBytes,
	}
	if err := sendHandshakeMessage(base, transcript, serverHello); err != nil {
		return nil, err
	}

	clientKey, clientKeyRaw, err := recvHandshakeMessage(base, "client_key")
	if err != nil {
		return nil, err
	}
	appendTranscript(transcript, clientKeyRaw)

	sharedSecret, err := scheme.Decapsulate(priv, clientKey.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decapsulate provider tunnel key: %w", err)
	}
	secrets, err := deriveTunnelSecrets(sharedSecret, transcript.Sum(nil))
	if err != nil {
		return nil, err
	}

	serverFinish := tunnelMessage{
		Magic:      tunnelProtocolMagic,
		Version:    tunnelProtocolV2,
		Type:       "server_finish",
		VerifyData: finishDigest(secrets.serverFinishKey, secrets.transcriptDigest),
	}
	if err := sendHandshakeMessage(base, nil, serverFinish); err != nil {
		return nil, err
	}

	clientFinish, _, err := recvHandshakeMessage(base, "client_finish")
	if err != nil {
		return nil, err
	}
	if err := verifyFinish(clientFinish.VerifyData, secrets.clientFinishKey, secrets.transcriptDigest); err != nil {
		return nil, fmt.Errorf("verify provider tunnel finish: %w", err)
	}

	return newSecureChannel(base, suite, secrets.serverKey, secrets.clientKey, secrets.serverNonceSeed, secrets.clientNonceSeed)
}

func newSecureChannel(base channel.Channel, suite string, sendKey, recvKey, sendNonceSeed, recvNonceSeed []byte) (channel.Channel, error) {
	sendAEAD, err := newTunnelAEAD(suite, sendKey, "send")
	if err != nil {
		return nil, err
	}
	recvAEAD, err := newTunnelAEAD(suite, recvKey, "recv")
	if err != nil {
		return nil, err
	}
	if len(sendNonceSeed) != 12 || len(recvNonceSeed) != 12 {
		return nil, fmt.Errorf("invalid tunnel nonce seed length")
	}

	secure := &secureChannel{base: base, sendAEAD: sendAEAD, recvAEAD: recvAEAD}
	copy(secure.sendNonceSeed[:], sendNonceSeed)
	copy(secure.recvNonceSeed[:], recvNonceSeed)
	return secure, nil
}

func newTunnelAEAD(suite string, key []byte, direction string) (cipher.AEAD, error) {
	switch suite {
	case tunnelSuiteHybridAES256GCM:
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, fmt.Errorf("create tunnel %s cipher: %w", direction, err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("create tunnel %s AEAD: %w", direction, err)
		}
		return aead, nil
	case tunnelSuiteHybridChaCha20Poly1305:
		aead, err := chacha20poly1305.New(key)
		if err != nil {
			return nil, fmt.Errorf("create tunnel %s chacha20-poly1305: %w", direction, err)
		}
		return aead, nil
	default:
		return nil, fmt.Errorf("unsupported executor tunnel suite %q", suite)
	}
}

func (c *secureChannel) Send(msg []byte) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.sendSeq == math.MaxUint64 {
		return errors.New("executor tunnel send sequence exhausted")
	}
	header := tunnelRecordHeader(c.sendSeq)
	nonce := tunnelNonce(c.sendNonceSeed, c.sendSeq)
	ciphertext := c.sendAEAD.Seal(nil, nonce[:], msg, header)
	record := append(header, ciphertext...)
	if err := c.base.Send(record); err != nil {
		return err
	}
	c.sendSeq++
	return nil
}

func (c *secureChannel) Recv() ([]byte, error) {
	c.recvMu.Lock()
	defer c.recvMu.Unlock()
	record, err := c.base.Recv()
	if err != nil {
		return nil, err
	}
	if len(record) < len(tunnelRecordMagic)+8 {
		return nil, errors.New("executor tunnel record too short")
	}
	header := record[:len(tunnelRecordMagic)+8]
	if string(header[:len(tunnelRecordMagic)]) != string(tunnelRecordMagic) {
		return nil, errors.New("executor tunnel record magic mismatch")
	}
	seq := binary.BigEndian.Uint64(header[len(tunnelRecordMagic):])
	if seq != c.recvSeq {
		return nil, fmt.Errorf("executor tunnel record sequence mismatch: got %d want %d", seq, c.recvSeq)
	}
	nonce := tunnelNonce(c.recvNonceSeed, seq)
	plaintext, err := c.recvAEAD.Open(nil, nonce[:], record[len(header):], header)
	if err != nil {
		return nil, fmt.Errorf("decrypt executor tunnel record: %w", err)
	}
	c.recvSeq++
	return plaintext, nil
}

func (c *secureChannel) Close() error {
	return c.base.Close()
}

func sendHandshakeMessage(base channel.Channel, transcript io.Writer, msg tunnelMessage) error {
	payload, err := marshalHandshakeMessage(msg)
	if err != nil {
		return err
	}
	if transcript != nil {
		appendTranscriptWriter(transcript, payload)
	}
	return base.Send(payload)
}

func recvHandshakeMessage(base channel.Channel, expectedType string) (tunnelMessage, []byte, error) {
	payload, err := base.Recv()
	if err != nil {
		return tunnelMessage{}, nil, err
	}
	msg, err := unmarshalHandshakeMessage(payload)
	if err != nil {
		return tunnelMessage{}, nil, err
	}
	if msg.Type != expectedType {
		return tunnelMessage{}, nil, fmt.Errorf("unexpected tunnel handshake message %q, want %q", msg.Type, expectedType)
	}
	return msg, payload, nil
}

func marshalHandshakeMessage(msg tunnelMessage) ([]byte, error) {
	msg.Magic = tunnelProtocolMagic
	if msg.Version == 0 {
		msg.Version = tunnelProtocolV2
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal tunnel handshake message %s: %w", msg.Type, err)
	}
	return payload, nil
}

func unmarshalHandshakeMessage(payload []byte) (tunnelMessage, error) {
	var msg tunnelMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		return tunnelMessage{}, fmt.Errorf("unmarshal tunnel handshake message: %w", err)
	}
	if msg.Magic != tunnelProtocolMagic {
		return tunnelMessage{}, fmt.Errorf("unexpected tunnel handshake magic %q", msg.Magic)
	}
	return msg, nil
}

func deriveTunnelSecrets(sharedSecret, transcriptDigest []byte) (*tunnelSecrets, error) {
	material := append(append([]byte(nil), sharedSecret...), transcriptDigest...)
	masterSecret := deriveTunnelBytes(material, "tf-nix 2026-05 tunnel master", 32)
	clientKey := deriveTunnelBytes(masterSecret, "tf-nix 2026-05 tunnel client key", 32)
	serverKey := deriveTunnelBytes(masterSecret, "tf-nix 2026-05 tunnel server key", 32)
	clientNonceSeed := deriveTunnelBytes(masterSecret, "tf-nix 2026-05 tunnel client nonce", 12)
	serverNonceSeed := deriveTunnelBytes(masterSecret, "tf-nix 2026-05 tunnel server nonce", 12)
	clientFinishKey := deriveTunnelBytes(masterSecret, "tf-nix 2026-05 tunnel client finish", 32)
	serverFinishKey := deriveTunnelBytes(masterSecret, "tf-nix 2026-05 tunnel server finish", 32)
	return &tunnelSecrets{
		clientKey:        clientKey,
		serverKey:        serverKey,
		clientNonceSeed:  clientNonceSeed,
		serverNonceSeed:  serverNonceSeed,
		clientFinishKey:  clientFinishKey,
		serverFinishKey:  serverFinishKey,
		transcriptDigest: append([]byte(nil), transcriptDigest...),
	}, nil
}

func deriveTunnelBytes(material []byte, label string, length int) []byte {
	out := make([]byte, length)
	blake3.DeriveKey(label, material, out)
	return out
}

func finishDigest(key, transcriptDigest []byte) []byte {
	digester, err := blake3.NewKeyed(key)
	if err != nil {
		panic(fmt.Sprintf("create keyed blake3 hasher: %v", err))
	}
	_, _ = digester.Write(transcriptDigest)
	return digester.Sum(nil)
}

func verifyFinish(got, key, transcriptDigest []byte) error {
	want := finishDigest(key, transcriptDigest)
	if len(got) != len(want) || subtle.ConstantTimeCompare(got, want) != 1 {
		return errors.New("finish digest mismatch")
	}
	return nil
}

func tunnelRecordHeader(seq uint64) []byte {
	header := make([]byte, 0, len(tunnelRecordMagic)+8)
	header = append(header, tunnelRecordMagic...)
	seqBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(seqBytes, seq)
	header = append(header, seqBytes...)
	return header
}

func tunnelNonce(seed [12]byte, seq uint64) [12]byte {
	nonce := seed
	binary.BigEndian.PutUint64(nonce[4:], seq)
	return nonce
}

func appendTranscript(digester io.Writer, payload []byte) {
	appendTranscriptWriter(digester, payload)
}

func appendTranscriptWriter(w io.Writer, payload []byte) {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(payload)))
	_, _ = w.Write(size[:])
	_, _ = w.Write(payload)
}

func randomBytes(length int) ([]byte, error) {
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func supportedTunnelSuites() []string {
	return tunnelSuitesByPreference(preferAESTunnel())
}

func tunnelSuitesByPreference(preferAES bool) []string {
	if preferAES {
		return []string{tunnelSuiteHybridAES256GCM, tunnelSuiteHybridChaCha20Poly1305}
	}
	return []string{tunnelSuiteHybridChaCha20Poly1305, tunnelSuiteHybridAES256GCM}
}

func selectTunnelSuite(offered, preferred []string) (string, error) {
	for _, candidate := range preferred {
		if containsString(offered, candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no common executor tunnel suite between offered %v and supported %v", offered, preferred)
}

func preferAESTunnel() bool {
	switch runtime.GOARCH {
	case "amd64":
		return cpu.X86.HasAES && cpu.X86.HasPCLMULQDQ
	case "arm64":
		return cpu.ARM64.HasAES && cpu.ARM64.HasPMULL
	case "arm":
		return cpu.ARM.HasAES && cpu.ARM.HasPMULL
	case "s390x":
		return cpu.S390X.HasAESGCM
	default:
		return false
	}
}

var _ kem.Scheme = hybrid.X25519MLKEM768()
