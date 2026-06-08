package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWebRootUsesExplicitPath(t *testing.T) {
	if got := resolveWebRoot("/tmp/custom-web"); got != "/tmp/custom-web" {
		t.Fatalf("expected explicit web root to win, got %q", got)
	}
}

func TestHasWebIndex(t *testing.T) {
	root := t.TempDir()
	if hasWebIndex(root) {
		t.Fatalf("expected empty dir to have no web index")
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<html></html>"), 0600); err != nil {
		t.Fatal(err)
	}
	if !hasWebIndex(root) {
		t.Fatalf("expected index.html to be detected")
	}
}

func TestObfuscateDSN(t *testing.T) {
	// obfuscateDSN strips credentials AND host:port, retaining only the scheme
	// prefix and DB path — more aggressive than just hiding the password.
	tests := []struct {
		in   string
		want string
	}{
		{
			in:   "postgres://alice:secret@db.example.com:5432/budgie",
			want: "postgres://****/budgie",
		},
		{
			in:   "postgres://user:pass@127.0.0.1:5432/mydb",
			want: "postgres://****/mydb",
		},
		// DSN without '@' (e.g. plain host:port/db without auth)
		{in: "localhost:5432/budgie", want: "[redacted]"},
		// DSN with '@' but no DB path component
		{
			in:   "postgres://u:p@host",
			want: "postgres://****/",
		},
	}
	for _, tt := range tests {
		got := obfuscateDSN(tt.in)
		if got != tt.want {
			t.Errorf("obfuscateDSN(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestResolveStorage(t *testing.T) {
	// Clean any pre-existing env var.
	os.Unsetenv("BUDGIE_POSTGRES_DSN")

	// Default: no DSN, no explicit storage → sqlite
	if s, dsn := resolveStorage("sqlite", ""); s != "sqlite" || dsn != "" {
		t.Errorf("default: got storage=%q dsn=%q", s, dsn)
	}

	// Explicit -storage postgres with flag DSN
	if s, dsn := resolveStorage("postgres", "postgres://u:p@host/db"); s != "postgres" || dsn != "postgres://u:p@host/db" {
		t.Errorf("explicit postgres flag: got storage=%q dsn=%q", s, dsn)
	}

	// Backwards compat: DSN via flag, storage still default "sqlite" → inferred postgres
	if s, dsn := resolveStorage("sqlite", "postgres://u:p@host/db"); s != "postgres" || dsn != "postgres://u:p@host/db" {
		t.Errorf("backwards compat (flag dsn): got storage=%q dsn=%q", s, dsn)
	}

	// DSN via environment variable
	os.Setenv("BUDGIE_POSTGRES_DSN", "postgres://env:secret@envhost/db")
	defer os.Unsetenv("BUDGIE_POSTGRES_DSN")
	if s, dsn := resolveStorage("sqlite", ""); s != "postgres" || dsn != "postgres://env:secret@envhost/db" {
		t.Errorf("env var: got storage=%q dsn=%q", s, dsn)
	}

	// Flag DSN overrides env var (flag is set → env not consulted)
	if s, dsn := resolveStorage("postgres", "postgres://flag:x@flaghost/db"); s != "postgres" || dsn != "postgres://flag:x@flaghost/db" {
		t.Errorf("flag overrides env: got storage=%q dsn=%q", s, dsn)
	}
}
