package creds

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func newTestStore(t *testing.T) *FileStore {
	dir := t.TempDir()
	return &FileStore{
		path:     filepath.Join(dir, "dbos", "credentials.json"),
		fallback: filepath.Join(dir, "tscreds"), // isolated, not the real cwd file
	}
}

func TestFileStoreRoundTrip(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.Load("prod"); err != ErrNotFound {
		t.Fatalf("Load on empty store = %v, want ErrNotFound", err)
	}

	in := &Creds{Token: "dbos_x", RefreshToken: "r", UserName: "alice", Organization: "acme", ExpiresAt: 123}
	if err := s.Save("prod", in); err != nil {
		t.Fatal(err)
	}
	if err := s.Save("local", &Creds{Token: "dbos_y"}); err != nil {
		t.Fatal(err)
	}

	out, err := s.Load("prod")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round-trip mismatch:\n in: %+v\nout: %+v", in, out)
	}

	// Deleting one profile leaves the other intact.
	if err := s.Delete("prod"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load("prod"); err != ErrNotFound {
		t.Errorf("Load after delete = %v, want ErrNotFound", err)
	}
	if _, err := s.Load("local"); err != nil {
		t.Errorf("local should survive prod's delete: %v", err)
	}
	if err := s.Delete("prod"); err != ErrNotFound {
		t.Errorf("delete of missing profile = %v, want ErrNotFound", err)
	}
}

func TestFileStoreMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file mode is not meaningful on Windows")
	}
	s := newTestStore(t)
	if err := s.Save("prod", &Creds{Token: "t"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("credentials file mode = %o, want 600", perm)
	}
}

func TestTSCredentialsFallback(t *testing.T) {
	s := newTestStore(t)
	tsBlob := `{"token":"dbos_ts","refreshToken":"rt","userName":"bob","organization":"globex"}`
	if err := os.WriteFile(s.fallback, []byte(tsBlob), 0o600); err != nil {
		t.Fatal(err)
	}

	// No entry in our file for "cloud" -> falls back to the dbos-cloud login.
	c, err := s.Load("cloud")
	if err != nil {
		t.Fatal(err)
	}
	if c.Token != "dbos_ts" || c.RefreshToken != "rt" || c.UserName != "bob" || c.Organization != "globex" {
		t.Errorf("TS fallback parsed wrong: %+v", c)
	}
	if c.ExpiresAt != 0 {
		t.Errorf("TS file has no expiresAt; got %d", c.ExpiresAt)
	}

	// Our file takes precedence over the fallback once present.
	if err := s.Save("cloud", &Creds{Token: "dbos_ours"}); err != nil {
		t.Fatal(err)
	}
	if c, _ = s.Load("cloud"); c.Token != "dbos_ours" {
		t.Errorf("our file should win over the fallback, got %q", c.Token)
	}

	// The TS file is never written.
	if got, _ := os.ReadFile(s.fallback); string(got) != tsBlob {
		t.Errorf("TS file was modified:\n%s", got)
	}
}

func TestTSFallbackEmptyTokenIgnored(t *testing.T) {
	s := newTestStore(t)
	if err := os.WriteFile(s.fallback, []byte(`{"token":"","userName":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load("cloud"); err != ErrNotFound {
		t.Errorf("empty-token TS file should be ignored, got %v", err)
	}
}
