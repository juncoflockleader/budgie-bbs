package sitemodel

import "testing"

func TestAssetRules(t *testing.T) {
	if got := NormalizeAssetName(" logo "); got != "logo" {
		t.Fatalf("NormalizeAssetName() = %q, want logo", got)
	}
	if !ValidAsset(" logo ") {
		t.Fatalf("ValidAsset() rejected a trimmed logo slot")
	}
	if ValidAsset("avatar") {
		t.Fatalf("ValidAsset() accepted an unsupported slot")
	}
	if got := AssetObjectKey(" banner ", 42); got != "site/banner-42.png" {
		t.Fatalf("AssetObjectKey() = %q, want site/banner-42.png", got)
	}
}

func TestEmptyAssetVersions(t *testing.T) {
	versions := EmptyAssetVersions()
	if len(versions) != 2 || versions["logo"] != 0 || versions["banner"] != 0 {
		t.Fatalf("EmptyAssetVersions() = %+v, want zeroed logo and banner slots", versions)
	}
	versions["logo"] = 99
	fresh := EmptyAssetVersions()
	if fresh["logo"] != 0 {
		t.Fatalf("EmptyAssetVersions() reused state, got %+v", fresh)
	}
}
