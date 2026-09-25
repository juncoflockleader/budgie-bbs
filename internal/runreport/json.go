package runreport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func WriteJSON(w io.Writer, value any, pretty bool) error {
	encoder := json.NewEncoder(w)
	if pretty {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(value)
}

func WriteJSONFile(path string, value any, pretty bool) error {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, value, pretty); err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func DecodeJSON[T any](r io.Reader, rejectUnknownFields bool) (T, error) {
	var out T
	decoder := json.NewDecoder(r)
	if rejectUnknownFields {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(&out); err != nil {
		return out, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return out, fmt.Errorf("unexpected trailing JSON")
	}
	return out, nil
}

func ReadJSONFile[T any](path string, rejectUnknownFields bool) (T, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		var out T
		return out, err
	}
	return DecodeJSON[T](bytes.NewReader(data), rejectUnknownFields)
}
