package sitemodel

import (
	"errors"
	"regexp"
	"strings"
)

const (
	DefaultSiteTitle = "Budgie BBS"
	DefaultSiteTheme = "light"
)

var validThemes = map[string]struct{}{
	"dark":  {},
	"dim":   {},
	"light": {},
	"warm":  {},
}

var hexColorRe = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

type AppearanceFields struct {
	SiteTitle     string
	Logo          string
	Tagline       string
	BannerMessage string
	AccentColor   string
	DefaultTheme  string
}

// Appearance holds admin-configurable site-wide branding/appearance. All fields
// are public so anonymous users can receive them for the login page and header.
type Appearance struct {
	SiteTitle     string `json:"siteTitle"`
	Logo          string `json:"logo"`
	Tagline       string `json:"tagline"`
	BannerMessage string `json:"bannerMessage"`
	AccentColor   string `json:"accentColor"`
	DefaultTheme  string `json:"defaultTheme"`
	// MainMenuLayout is the admin-configurable SSH/TUI main-menu composition.
	// Nil means "use the built-in default layout".
	MainMenuLayout *TUIMainMenuLayout `json:"mainMenuLayout,omitempty"`
	UpdatedAt      int64              `json:"updatedAt"`
}

func (a Appearance) Fields() AppearanceFields {
	return AppearanceFields{
		SiteTitle:     a.SiteTitle,
		Logo:          a.Logo,
		Tagline:       a.Tagline,
		BannerMessage: a.BannerMessage,
		AccentColor:   a.AccentColor,
		DefaultTheme:  a.DefaultTheme,
	}
}

func (a *Appearance) ApplyFields(fields AppearanceFields) {
	a.SiteTitle = fields.SiteTitle
	a.Logo = fields.Logo
	a.Tagline = fields.Tagline
	a.BannerMessage = fields.BannerMessage
	a.AccentColor = fields.AccentColor
	a.DefaultTheme = fields.DefaultTheme
}

func DefaultAppearanceFields() AppearanceFields {
	return AppearanceFields{
		SiteTitle:    DefaultSiteTitle,
		DefaultTheme: DefaultSiteTheme,
	}
}

func ApplyAppearanceDefaults(fields AppearanceFields) AppearanceFields {
	if strings.TrimSpace(fields.SiteTitle) == "" {
		fields.SiteTitle = DefaultSiteTitle
	}
	if !validTheme(fields.DefaultTheme) {
		fields.DefaultTheme = DefaultSiteTheme
	}
	return fields
}

func NormalizeAppearanceFields(fields AppearanceFields) (AppearanceFields, error) {
	fields.SiteTitle = strings.TrimSpace(fields.SiteTitle)
	if fields.SiteTitle == "" {
		fields.SiteTitle = DefaultSiteTitle
	}
	if len(fields.SiteTitle) > 80 {
		return AppearanceFields{}, errors.New("site title must be 80 characters or less")
	}
	fields.Logo = strings.TrimSpace(fields.Logo)
	if len(fields.Logo) > 32 {
		return AppearanceFields{}, errors.New("logo must be 32 bytes or less (a short glyph or emoji)")
	}
	fields.Tagline = strings.TrimSpace(fields.Tagline)
	if len(fields.Tagline) > 200 {
		return AppearanceFields{}, errors.New("tagline must be 200 characters or less")
	}
	fields.BannerMessage = strings.TrimSpace(fields.BannerMessage)
	if len(fields.BannerMessage) > 500 {
		return AppearanceFields{}, errors.New("banner must be 500 characters or less")
	}
	fields.AccentColor = strings.ToLower(strings.TrimSpace(fields.AccentColor))
	if fields.AccentColor != "" && !hexColorRe.MatchString(fields.AccentColor) {
		return AppearanceFields{}, errors.New("accent color must be a hex value like #58a6ff")
	}
	fields.DefaultTheme = strings.TrimSpace(fields.DefaultTheme)
	if fields.DefaultTheme == "" {
		fields.DefaultTheme = DefaultSiteTheme
	}
	if !validTheme(fields.DefaultTheme) {
		return AppearanceFields{}, errors.New("default theme must be one of dark, dim, light, warm")
	}
	return fields, nil
}

func validTheme(theme string) bool {
	_, ok := validThemes[theme]
	return ok
}
