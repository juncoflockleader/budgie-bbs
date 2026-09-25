package sitemodel

import (
	"fmt"
	"strings"
)

var validAssets = map[string]struct{}{
	"logo":   {},
	"banner": {},
}

func NormalizeAssetName(name string) string {
	return strings.TrimSpace(name)
}

func ValidAsset(name string) bool {
	_, ok := validAssets[NormalizeAssetName(name)]
	return ok
}

// AssetObjectKey is the versioned external-store key for immutable CDN reads.
func AssetObjectKey(name string, version int64) string {
	return fmt.Sprintf("site/%s-%d.png", NormalizeAssetName(name), version)
}

func EmptyAssetVersions() map[string]int64 {
	out := make(map[string]int64, len(validAssets))
	for name := range validAssets {
		out[name] = 0
	}
	return out
}
