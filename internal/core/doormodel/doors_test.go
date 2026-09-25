package doormodel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigEmpty(t *testing.T) {
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("expected nil error for empty path, got %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil config for empty path, got %+v", cfg)
	}
}

func TestLoadConfigNotFound(t *testing.T) {
	_, err := LoadConfig("/tmp/does-not-exist-doors.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadConfigValid(t *testing.T) {
	data := `{
		"doors": [
			{"id":"tw", "name":"Trade Wars", "cmd":"/bin/tw", "args":["-d","/data"]},
			{"id":"lord", "name":"LORD", "description":"Legend of the Red Dragon", "cmd":"/bin/lord"}
		]
	}`
	f, err := os.CreateTemp(t.TempDir(), "doors-*.json")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(data); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	_ = f.Close()

	cfg, err := LoadConfig(f.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if len(cfg.Doors) != 2 {
		t.Fatalf("expected 2 doors, got %d", len(cfg.Doors))
	}
	if cfg.Doors[0].ID != "tw" || cfg.Doors[0].Cmd != "/bin/tw" {
		t.Errorf("unexpected door 0: %+v", cfg.Doors[0])
	}
	if cfg.Doors[1].Description != "Legend of the Red Dragon" {
		t.Errorf("expected description, got %q", cfg.Doors[1].Description)
	}
	if len(cfg.Doors[0].Args) != 2 || cfg.Doors[0].Args[0] != "-d" {
		t.Errorf("unexpected args: %v", cfg.Doors[0].Args)
	}
}

func TestLoadConfigMalformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}
