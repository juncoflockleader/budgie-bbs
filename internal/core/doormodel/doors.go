package doormodel

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// DoorConfig describes a single door game entry that sysops configure.
type DoorConfig struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Cmd         string   `json:"cmd"`
	Args        []string `json:"args,omitempty"`
}

// DoorsConfig is the top-level structure of a doors.json config file.
type DoorsConfig struct {
	Doors []DoorConfig `json:"doors"`
}

// LoadConfig reads and parses a doors.json file from the given path. Returns
// nil, nil when path is empty (doors are simply disabled).
func LoadConfig(path string) (*DoorsConfig, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("doors config not found: %s", path)
		}
		return nil, fmt.Errorf("read doors config: %w", err)
	}
	var cfg DoorsConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse doors config: %w", err)
	}
	return &cfg, nil
}
