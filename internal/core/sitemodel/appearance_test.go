package sitemodel

import (
	"strings"
	"testing"
)

func TestNormalizeAppearanceFields(t *testing.T) {
	fields, err := NormalizeAppearanceFields(AppearanceFields{
		SiteTitle:     " Campus Hub ",
		Logo:          " * ",
		Tagline:       " for students ",
		BannerMessage: " Welcome ",
		AccentColor:   " #FF8800 ",
		DefaultTheme:  "warm",
	})
	if err != nil {
		t.Fatalf("NormalizeAppearanceFields: %v", err)
	}
	if fields.SiteTitle != "Campus Hub" || fields.Logo != "*" || fields.Tagline != "for students" ||
		fields.BannerMessage != "Welcome" || fields.AccentColor != "#ff8800" || fields.DefaultTheme != "warm" {
		t.Fatalf("normalized appearance = %+v", fields)
	}
}

func TestNormalizeAppearanceFieldsDefaultsAndValidation(t *testing.T) {
	fields, err := NormalizeAppearanceFields(AppearanceFields{})
	if err != nil {
		t.Fatalf("NormalizeAppearanceFields defaults: %v", err)
	}
	if fields.SiteTitle != DefaultSiteTitle || fields.DefaultTheme != DefaultSiteTheme {
		t.Fatalf("defaults = %+v, want title/theme defaults", fields)
	}
	if _, err := NormalizeAppearanceFields(AppearanceFields{SiteTitle: strings.Repeat("x", 81)}); err == nil {
		t.Fatalf("long site title accepted")
	}
	if _, err := NormalizeAppearanceFields(AppearanceFields{AccentColor: "blue"}); err == nil {
		t.Fatalf("invalid accent accepted")
	}
	if _, err := NormalizeAppearanceFields(AppearanceFields{DefaultTheme: "neon"}); err == nil {
		t.Fatalf("invalid theme accepted")
	}
}

func TestApplyAppearanceDefaults(t *testing.T) {
	fields := ApplyAppearanceDefaults(AppearanceFields{SiteTitle: " ", DefaultTheme: "unknown"})
	if fields.SiteTitle != DefaultSiteTitle || fields.DefaultTheme != DefaultSiteTheme {
		t.Fatalf("ApplyAppearanceDefaults() = %+v, want title/theme defaults", fields)
	}
}
