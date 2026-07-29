package creds

import (
	"encoding/json"
	"os"
)

// loadTSCredentials reads a dbos-cloud (TypeScript CLI) credentials file — a
// single JSON login blob at a cwd-relative ./.dbos/credentials. This is
// read-only compatibility: we parse the format but never write it, so an
// existing dbos-cloud login carries over. Returns (nil, nil) when the file is
// absent or carries no token.
func loadTSCredentials(path string) (*Creds, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Creds' JSON tags match the TS keys (token/refreshToken/userName/
	// organization), so the same struct decodes it; there is no expiresAt.
	var c Creds
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Token == "" {
		return nil, nil
	}
	return &c, nil
}
