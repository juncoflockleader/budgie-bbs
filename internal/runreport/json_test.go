package runreport

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type decodeJSONTestReport struct {
	OK int `json:"ok"`
}

func TestWriteJSON(t *testing.T) {
	var compact bytes.Buffer
	if err := WriteJSON(&compact, map[string]int{"ok": 1}, false); err != nil {
		t.Fatalf("compact json: %v", err)
	}
	if compact.String() != "{\"ok\":1}\n" {
		t.Fatalf("compact json = %q", compact.String())
	}

	var pretty bytes.Buffer
	if err := WriteJSON(&pretty, map[string]int{"ok": 1}, true); err != nil {
		t.Fatalf("pretty json: %v", err)
	}
	if pretty.String() != "{\n  \"ok\": 1\n}\n" {
		t.Fatalf("pretty json = %q", pretty.String())
	}
}

func TestWriteJSONFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "report.json")
	if err := WriteJSONFile(path, map[string]int{"ok": 1}, true); err != nil {
		t.Fatalf("write json file: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read json file: %v", err)
	}
	if string(raw) != "{\n  \"ok\": 1\n}\n" {
		t.Fatalf("json file = %q", string(raw))
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary file still exists or stat failed: %v", err)
	}
}

func TestDecodeJSONRejectsUnknownAndTrailingData(t *testing.T) {
	report, err := DecodeJSON[decodeJSONTestReport](strings.NewReader(`{"ok":1}`), true)
	if err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if report.OK != 1 {
		t.Fatalf("report = %+v, want ok=1", report)
	}

	if _, err := DecodeJSON[decodeJSONTestReport](strings.NewReader(`{"ok":1,"extra":2}`), true); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("decode unknown-field err = %v, want unknown field", err)
	}
	if _, err := DecodeJSON[decodeJSONTestReport](strings.NewReader(`{"ok":1,"extra":2}`), false); err != nil {
		t.Fatalf("decode lenient unknown field: %v", err)
	}
	if _, err := DecodeJSON[decodeJSONTestReport](strings.NewReader(`{"ok":1} {}`), true); err == nil || !strings.Contains(err.Error(), "unexpected trailing JSON") {
		t.Fatalf("decode trailing err = %v, want trailing-data error", err)
	}
}

func TestReadJSONFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(path, []byte(`{"ok":2}`), 0o644); err != nil {
		t.Fatalf("write json file: %v", err)
	}
	report, err := ReadJSONFile[decodeJSONTestReport](path, true)
	if err != nil {
		t.Fatalf("read json file: %v", err)
	}
	if report.OK != 2 {
		t.Fatalf("report = %+v, want ok=2", report)
	}
}
