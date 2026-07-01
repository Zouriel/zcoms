package instagram

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// The sidecar holds the live Instagram session in memory but persists nothing.
// We own the session blob (instagrapi's settings dump) and store it encrypted at
// rest so a leaked backup of the config dir alone does not hand over the account.
// AES-256-GCM with a 32-byte key kept in a sibling 0600 file (key separation:
// the ciphertext and the key are two files, so copying just one is useless).

type sessionStore struct {
	blobPath string // session.enc
	keyPath  string // session.key
}

func newSessionStore(dir string) *sessionStore {
	base := filepath.Join(dir, "instagram")
	return &sessionStore{
		blobPath: filepath.Join(base, "session.enc"),
		keyPath:  filepath.Join(base, "session.key"),
	}
}

// loadKey reads the account key, generating a fresh one on first use.
func (s *sessionStore) loadKey() ([]byte, error) {
	if b, err := os.ReadFile(s.keyPath); err == nil {
		if len(b) == 32 {
			return b, nil
		}
		return nil, errors.New("instagram session key is corrupt (expected 32 bytes)")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(s.keyPath), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(s.keyPath, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// Save encrypts the raw settings JSON to session.enc.
func (s *sessionStore) Save(settings []byte) error {
	key, err := s.loadKey()
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	ct := gcm.Seal(nonce, nonce, settings, nil)
	if err := os.MkdirAll(filepath.Dir(s.blobPath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.blobPath, ct, 0o600)
}

// Load decrypts session.enc. It returns (nil, nil) when no session is stored yet,
// so the caller falls back to a fresh username/password login.
func (s *sessionStore) Load() ([]byte, error) {
	ct, err := os.ReadFile(s.blobPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	key, err := s.loadKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ct) < gcm.NonceSize() {
		return nil, errors.New("instagram session blob is truncated")
	}
	nonce, body := ct[:gcm.NonceSize()], ct[gcm.NonceSize():]
	return gcm.Open(nil, nonce, body, nil)
}

// Clear removes the stored session (on logout).
func (s *sessionStore) Clear() error {
	err := os.Remove(s.blobPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
