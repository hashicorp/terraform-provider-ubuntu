package runtime

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

const journalKeyEnvVar = "TF_NIX_JOURNAL_KEY_B64"

var runtimeJournalKey struct {
	mu  sync.RWMutex
	key []byte
}

type journalEnvelope struct {
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func setRuntimeJournalKey(key []byte) error {
	if len(key) != 32 {
		return fmt.Errorf("journal key must be 32 bytes, got %d", len(key))
	}
	cloned := append([]byte(nil), key...)
	runtimeJournalKey.mu.Lock()
	defer runtimeJournalKey.mu.Unlock()
	runtimeJournalKey.key = cloned
	return nil
}

func runtimeJournalKeyBytes() ([]byte, error) {
	runtimeJournalKey.mu.RLock()
	defer runtimeJournalKey.mu.RUnlock()
	if len(runtimeJournalKey.key) == 0 {
		return nil, errors.New("journal key is not configured")
	}
	return append([]byte(nil), runtimeJournalKey.key...), nil
}

func runtimeJournalKeyEnv() (string, error) {
	key, err := runtimeJournalKeyBytes()
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

func loadRuntimeJournalKeyFromEnv() error {
	encoded := os.Getenv(journalKeyEnvVar)
	if encoded == "" {
		return errors.New("journal key environment is not set")
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("decode journal key: %w", err)
	}
	return setRuntimeJournalKey(key)
}

func marshalEncryptedJSON(value any) ([]byte, error) {
	key, err := runtimeJournalKeyBytes()
	if err != nil {
		return nil, err
	}
	plaintext, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	env := journalEnvelope{
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(aead.Seal(nil, nonce, plaintext, nil)),
	}
	return json.Marshal(env)
}

func unmarshalEncryptedJSON(data []byte, target any) error {
	key, err := runtimeJournalKeyBytes()
	if err != nil {
		return err
	}
	var env journalEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(plaintext, target)
}
