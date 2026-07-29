// Package creds stores per-profile credentials behind a Store interface, so the
// backend can change (an OS keychain, later) without touching call sites. The
// v1 backend is a 0600 JSON file beside the config, with a read-only fallback to
// an existing dbos-cloud (TypeScript CLI) login.
package creds

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNotFound means no credentials are stored for the profile.
var ErrNotFound = errors.New("no stored credentials")

// Creds is a stored login. The JSON tags match the dbos-cloud TS CLI's
// credentials file, so the same shape parses both our per-profile entries and
// that file.
type Creds struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refreshToken,omitempty"`
	UserName     string `json:"userName,omitempty"`
	Organization string `json:"organization,omitempty"`
	// ExpiresAt is Unix seconds, or 0 when unknown. Not present in the TS file.
	ExpiresAt int64 `json:"expiresAt,omitempty"`
}

// Store persists credentials keyed by profile name.
type Store interface {
	Load(profile string) (*Creds, error)
	Save(profile string, c *Creds) error
	Delete(profile string) error
}

// FileStore is the v1 Store: a 0600 JSON file keyed by profile, with a
// read-only fallback to the TS CLI's cwd-relative ./.dbos/credentials.
type FileStore struct {
	path     string // our credentials.json (read/write)
	fallback string // TS ./.dbos/credentials (read-only; "" disables)
}

// NewFileStore builds the file-backed store at os.UserConfigDir()/dbos/, with
// the read-only dbos-cloud fallback at the cwd-relative ./.dbos/credentials.
func NewFileStore() (*FileStore, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("locating user config dir: %w", err)
	}
	return &FileStore{
		path:     filepath.Join(dir, "dbos", "credentials.json"),
		fallback: filepath.Join(".dbos", "credentials"),
	}, nil
}

// Load returns the profile's credentials, falling back (read-only) to an
// existing dbos-cloud login, or ErrNotFound if neither has them.
func (s *FileStore) Load(profile string) (*Creds, error) {
	all, err := s.readAll()
	if err != nil {
		return nil, err
	}
	if c, ok := all[profile]; ok {
		return &c, nil
	}
	if s.fallback != "" {
		if c, err := loadTSCredentials(s.fallback); err == nil && c != nil {
			return c, nil
		}
	}
	return nil, ErrNotFound
}

// Save writes the profile's credentials, leaving other profiles untouched.
func (s *FileStore) Save(profile string, c *Creds) error {
	all, err := s.readAll()
	if err != nil {
		return err
	}
	all[profile] = *c
	return s.writeAll(all)
}

// Delete removes the profile's credentials, or returns ErrNotFound.
func (s *FileStore) Delete(profile string) error {
	all, err := s.readAll()
	if err != nil {
		return err
	}
	if _, ok := all[profile]; !ok {
		return ErrNotFound
	}
	delete(all, profile)
	return s.writeAll(all)
}

func (s *FileStore) readAll() (map[string]Creds, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Creds{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.path, err)
	}
	var all map[string]Creds
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", s.path, err)
	}
	if all == nil {
		all = map[string]Creds{}
	}
	return all, nil
}

// writeAll writes the credential file atomically at mode 0600. On Windows the
// mode is a near no-op (the file inherits the directory ACL) — see the Config
// and credentials section of AGENTS.md.
func (s *FileStore) writeAll(all map[string]Creds) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".credentials-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}
