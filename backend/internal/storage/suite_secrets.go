package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// File contents are AES-256-GCM encrypted; SQLite contains only opaque handles.
// The master key, encrypted files and directory are private to the Backend UID.
type suiteSecretVault struct {
	dir  string
	aead cipher.AEAD
}

var suiteSecretName = regexp.MustCompile(`^secret_[A-Za-z0-9_-]+$`)

func openSuiteSecretVault(dataDir string, existingAuthority bool) (*suiteSecretVault, error) {
	dir := filepath.Join(dataDir, "secrets")
	if err := privateSuiteDir(dir); err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "suite")
	if err := privateSuiteDir(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "master.key")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			return nil, readErr
		}
		if existingAuthority || len(entries) != 0 {
			return nil, fmt.Errorf("Suite secret-store key is missing; restore the complete data backup, refusing key regeneration")
		}
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		var f *os.File
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			_, err = f.Write(key)
			if err == nil {
				err = f.Sync()
			}
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
		}
		if err != nil {
			return nil, err
		}
		if err := syncSuiteDir(dir); err != nil {
			return nil, err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return nil, fmt.Errorf("Suite master.key must be a regular private 0600 file")
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("invalid Suite secret-store key; restore the complete data backup")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &suiteSecretVault{dir, aead}, nil
}
func privateSuiteDir(path string) error {
	if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 {
		return fmt.Errorf("Suite secrets directory must be a private 0700 directory")
	}
	return nil
}
func syncSuiteDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (v *suiteSecretVault) put(handle string, plaintext []byte) error {
	if !suiteSecretName.MatchString(handle) {
		return fmt.Errorf("invalid secret handle")
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	ciphertext := v.aead.Seal(nonce, nonce, plaintext, []byte("nox-suite-v1:"+handle))
	f, err := os.CreateTemp(v.dir, ".pending-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(ciphertext)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	destination := filepath.Join(v.dir, handle)
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return fmt.Errorf("secret handle already exists")
	}
	if err := os.Link(name, destination); err != nil {
		return err
	}
	return syncSuiteDir(v.dir)
}
func (v *suiteSecretVault) get(handle string) ([]byte, error) {
	if !suiteSecretName.MatchString(handle) {
		return nil, fmt.Errorf("invalid secret handle")
	}
	path := filepath.Join(v.dir, handle)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 4<<20 {
		return nil, fmt.Errorf("invalid Suite secret file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	n := v.aead.NonceSize()
	if len(data) < n {
		return nil, io.ErrUnexpectedEOF
	}
	return v.aead.Open(nil, data[:n], data[n:], []byte("nox-suite-v1:"+handle))
}
func (v *suiteSecretVault) remove(handle string) {
	if suiteSecretName.MatchString(handle) {
		_ = os.Remove(filepath.Join(v.dir, handle))
	}
}
