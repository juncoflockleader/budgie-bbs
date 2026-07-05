package core

import "github.com/juncoflockleader/budgie-bbs/internal/core/doormodel"

type DoorConfig = doormodel.DoorConfig
type DoorsConfig = doormodel.DoorsConfig

// LoadDoorsConfig reads and parses a doors.json file from the given path.
// Returns nil, nil when path is empty (doors are simply disabled).
// Returns an error if the file exists but cannot be parsed.
func LoadDoorsConfig(path string) (*DoorsConfig, error) {
	return doormodel.LoadConfig(path)
}
