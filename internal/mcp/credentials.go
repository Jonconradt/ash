package mcp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

var errCredentialNotFound = errors.New("MCP credentials not found")

// CredentialStorage stores OAuth credentials without exposing them to ash.
type CredentialStorage interface {
	Get(server string) ([]byte, error)
	Set(server string, value []byte) error
}

type credentialStorage = CredentialStorage

type systemCredentialStorage struct {
	directory string
	key       []byte
	native    nativeCredentialStore
}

type nativeCredentialStore interface {
	Get(service, user string) (string, error)
	Set(service, user, password string) error
}

type keyringCredentialStore struct{}

func (keyringCredentialStore) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (keyringCredentialStore) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}

// NewCredentialStorage uses the OS credential manager and an encrypted-file
// fallback when a high-entropy key is available.
func NewCredentialStorage(directory, rawKey string) (CredentialStorage, error) {
	key, err := decodeCredentialKey(rawKey)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("creating MCP credential directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, fmt.Errorf("checking MCP credential directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("MCP credential path must be a real directory")
	}
	// #nosec G302 -- Directories require execute permission; 0700 restricts access to the owner.
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, fmt.Errorf("securing MCP credential directory: %w", err)
	}
	return &systemCredentialStorage{directory: directory, key: key, native: keyringCredentialStore{}}, nil
}

func decodeCredentialKey(raw string) ([]byte, error) {
	if raw == "" {
		return nil, nil
	}
	if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	for _, encoding := range []*base64.Encoding{base64.RawStdEncoding, base64.StdEncoding, base64.RawURLEncoding, base64.URLEncoding} {
		if decoded, err := encoding.DecodeString(raw); err == nil && len(decoded) == 32 {
			return decoded, nil
		}
	}
	return nil, errors.New("ASH_MCP_CREDENTIAL_KEY must be a 32-byte key encoded as hex or base64")
}

func (s *systemCredentialStorage) Get(server string) ([]byte, error) {
	value, err := s.native.Get("ash-mcp-oauth", server)
	if err == nil {
		return []byte(value), nil
	}
	if !errors.Is(err, keyring.ErrNotFound) {
		if fallback, fallbackErr := s.getEncrypted(server); fallbackErr == nil || !errors.Is(fallbackErr, errCredentialNotFound) {
			return fallback, fallbackErr
		}
		return nil, errCredentialNotFound
	}
	return s.getEncrypted(server)
}

func (s *systemCredentialStorage) Set(server string, value []byte) error {
	if err := s.native.Set("ash-mcp-oauth", server, string(value)); err == nil {
		return nil
	}
	if len(s.key) != 32 {
		return errors.New("OS credential store unavailable; set ASH_MCP_CREDENTIAL_KEY to a random 32-byte hex or base64 key to enable encrypted fallback storage")
	}
	records, err := s.readEncrypted()
	if err != nil && !errors.Is(err, errCredentialNotFound) {
		return err
	}
	if records == nil {
		records = make(map[string]string)
	}
	records[server] = base64.RawStdEncoding.EncodeToString(value)
	plaintext, err := json.Marshal(records)
	if err != nil {
		return fmt.Errorf("encoding encrypted MCP credential records: %w", err)
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return fmt.Errorf("initializing MCP credential encryption: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("initializing MCP credential encryption: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("generating MCP credential nonce: %w", err)
	}
	sealed := aead.Seal(nil, nonce, plaintext, []byte("ash-mcp-oauth-v1"))
	ciphertext := make([]byte, 0, len(nonce)+len(sealed))
	ciphertext = append(ciphertext, nonce...)
	ciphertext = append(ciphertext, sealed...)
	return s.writeEncrypted(ciphertext)
}

func (s *systemCredentialStorage) getEncrypted(server string) ([]byte, error) {
	records, err := s.readEncrypted()
	if err != nil {
		return nil, err
	}
	encoded, ok := records[server]
	if !ok {
		return nil, errCredentialNotFound
	}
	value, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("encrypted MCP credential record is malformed")
	}
	return value, nil
}

func (s *systemCredentialStorage) readEncrypted() (map[string]string, error) {
	root, err := os.OpenRoot(s.directory)
	if err != nil {
		return nil, fmt.Errorf("opening encrypted MCP credential directory: %w", err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			slog.Warn("Failed to close MCP credential directory", "error", err, "EID", "i0yVm0gL")
		}
	}()
	const filename = "mcp-credentials.enc"
	info, err := root.Lstat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errCredentialNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("checking encrypted MCP credentials: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("encrypted MCP credential file must be a private regular file")
	}
	if len(s.key) != 32 {
		return nil, errors.New("encrypted MCP credentials exist but ASH_MCP_CREDENTIAL_KEY is unavailable")
	}
	file, err := root.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("opening encrypted MCP credentials: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.Warn("Failed to close encrypted MCP credentials", "error", err, "EID", "VNWCMA6k")
		}
	}()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("checking opened MCP credentials: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || openedInfo.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("encrypted MCP credential file must be a private regular file")
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("reading encrypted MCP credentials: %w", err)
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, fmt.Errorf("initializing MCP credential decryption: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initializing MCP credential decryption: %w", err)
	}
	if len(data) < aead.NonceSize()+aead.Overhead() {
		return nil, errors.New("encrypted MCP credential file is truncated")
	}
	nonce, ciphertext := data[:aead.NonceSize()], data[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ciphertext, []byte("ash-mcp-oauth-v1"))
	if err != nil {
		return nil, errors.New("could not decrypt MCP credentials; check ASH_MCP_CREDENTIAL_KEY")
	}
	var records map[string]string
	if err := json.Unmarshal(plaintext, &records); err != nil {
		return nil, errors.New("decrypted MCP credentials are malformed")
	}
	return records, nil
}

func (s *systemCredentialStorage) writeEncrypted(data []byte) error {
	path := filepath.Join(s.directory, "mcp-credentials.enc")
	temp, err := os.CreateTemp(s.directory, ".mcp-credentials-*")
	if err != nil {
		return fmt.Errorf("creating encrypted MCP credential file: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("securing encrypted MCP credential file: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("writing encrypted MCP credentials: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("syncing encrypted MCP credentials: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("closing encrypted MCP credentials: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("installing encrypted MCP credentials: %w", err)
	}
	directory, err := os.Open(s.directory)
	if err != nil {
		return fmt.Errorf("opening MCP credential directory for sync: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return fmt.Errorf("syncing MCP credential directory: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("closing MCP credential directory: %w", closeErr)
	}
	return nil
}
