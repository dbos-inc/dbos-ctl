// Package config manages named connection profiles in
// os.UserConfigDir()/dbos/config.yaml and resolves effective settings from the
// flag > env > profile precedence chain.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Auth is the authentication axis of a profile. There is no mode enum; behavior
// is a function of auth + url + oidc (see the Config and credentials section of
// AGENTS.md).
type Auth string

const (
	AuthNone   Auth = "none"
	AuthBearer Auth = "bearer"
)

// OIDC holds the OpenID Connect settings a bearer profile needs to log in.
type OIDC struct {
	Issuer   string `yaml:"issuer,omitempty"`
	Audience string `yaml:"audience,omitempty"`
	ClientID string `yaml:"clientID,omitempty"`
}

// Profile is a named connection target. URL is the full API base including any
// reverse-proxy prefix (/conductor, /api/conductor, or none), used verbatim as
// the client Server.
type Profile struct {
	Auth Auth   `yaml:"auth,omitempty"`
	URL  string `yaml:"url,omitempty"`
	// Domain marks a DBOS-managed profile: it derives URL
	// (https://domain/conductor), bearer auth, and the OIDC tenant.
	// cloud.dbos.dev is production; any other value targets a non-production
	// cluster (undocumented).
	Domain string `yaml:"domain,omitempty"`
	Org    string `yaml:"org,omitempty"`
	App    string `yaml:"app,omitempty"`
	OIDC   *OIDC  `yaml:"oidc,omitempty"`
}

// File is the on-disk config: named profiles plus a pointer to the active one.
type File struct {
	Current  string             `yaml:"current,omitempty"`
	Profiles map[string]Profile `yaml:"profiles,omitempty"`
}

// Path returns the config file path: os.UserConfigDir()/dbos/config.yaml.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating user config dir: %w", err)
	}
	return filepath.Join(dir, "dbos", "config.yaml"), nil
}

// Load reads the config file, returning an empty config when it does not exist.
func Load() (*File, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return loadFrom(path)
}

func loadFrom(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &File{Profiles: map[string]Profile{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if f.Profiles == nil {
		f.Profiles = map[string]Profile{}
	}
	return &f, nil
}

// Save writes the config file, creating the directory as needed. The write is
// atomic (temp file + rename) so a crash mid-write cannot corrupt the config.
func Save(f *File) error {
	path, err := Path()
	if err != nil {
		return err
	}
	return saveTo(path, f)
}

func saveTo(path string, f *File) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.yaml")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
