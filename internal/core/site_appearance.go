package core

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/sitemodel"
)

// SiteAppearance holds admin-configurable site-wide branding/appearance. All
// fields are public (served to anonymous users for the login page and header).
type SiteAppearance struct {
	SiteTitle     string `json:"siteTitle"`
	Logo          string `json:"logo"` // short logo glyph/emoji shown before the title (e.g. 🐦)
	Tagline       string `json:"tagline"`
	BannerMessage string `json:"bannerMessage"`
	AccentColor   string `json:"accentColor"`  // "" or #rgb/#rrggbb
	DefaultTheme  string `json:"defaultTheme"` // dark|dim|light|warm
	// MainMenuLayout is the admin-configurable SSH/TUI main-menu composition.
	// Nil means "use the built-in default layout".
	MainMenuLayout *TUIMainMenuLayout `json:"mainMenuLayout,omitempty"`
	UpdatedAt      int64              `json:"updatedAt"`
}

// SiteAppearance returns the current site appearance settings (sensible defaults
// if unset).
func (c *Core) SiteAppearance() (*SiteAppearance, error) {
	defaults := sitemodel.DefaultAppearanceFields()
	out := &SiteAppearance{SiteTitle: defaults.SiteTitle, DefaultTheme: defaults.DefaultTheme}
	var layoutRaw string
	err := qQueryRow(c.DB,
		`SELECT site_title, COALESCE(logo,''), tagline, banner_message, accent_color, default_theme,
		        COALESCE(tui_main_menu_layout,''), updated_at
		   FROM site_appearance_settings WHERE id='default'`,
	).Scan(&out.SiteTitle, &out.Logo, &out.Tagline, &out.BannerMessage, &out.AccentColor, &out.DefaultTheme, &layoutRaw, &out.UpdatedAt)
	if err == sql.ErrNoRows {
		layout := defaultTUIMainMenuLayout()
		out.MainMenuLayout = &layout
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	layout := decodeTUIMainMenuLayout(layoutRaw)
	out.MainMenuLayout = &layout
	fields := sitemodel.ApplyAppearanceDefaults(siteAppearanceFields(*out))
	applySiteAppearanceFields(out, fields)
	return out, nil
}

// SetSiteAppearance validates and persists the site appearance settings.
func (c *Core) SetSiteAppearance(a SiteAppearance) (*SiteAppearance, error) {
	fields, err := sitemodel.NormalizeAppearanceFields(siteAppearanceFields(a))
	if err != nil {
		return nil, err
	}
	normLayout, err := normalizeTUIMainMenuLayout(a.MainMenuLayout)
	if err != nil {
		return nil, err
	}
	layoutRaw, err := encodeTUIMainMenuLayout(normLayout)
	if err != nil {
		return nil, err
	}
	if _, err := qExec(c.DB,
		`INSERT INTO site_appearance_settings (id, site_title, logo, tagline, banner_message, accent_color, default_theme, tui_main_menu_layout, updated_at)
		 VALUES ('default', ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   site_title=excluded.site_title, logo=excluded.logo, tagline=excluded.tagline, banner_message=excluded.banner_message,
		   accent_color=excluded.accent_color, default_theme=excluded.default_theme,
		   tui_main_menu_layout=excluded.tui_main_menu_layout, updated_at=excluded.updated_at`,
		fields.SiteTitle, fields.Logo, fields.Tagline, fields.BannerMessage, fields.AccentColor, fields.DefaultTheme, layoutRaw, nowMS(),
	); err != nil {
		return nil, err
	}
	return c.SiteAppearance()
}

func siteAppearanceFields(a SiteAppearance) sitemodel.AppearanceFields {
	return sitemodel.AppearanceFields{
		SiteTitle:     a.SiteTitle,
		Logo:          a.Logo,
		Tagline:       a.Tagline,
		BannerMessage: a.BannerMessage,
		AccentColor:   a.AccentColor,
		DefaultTheme:  a.DefaultTheme,
	}
}

func applySiteAppearanceFields(a *SiteAppearance, fields sitemodel.AppearanceFields) {
	a.SiteTitle = fields.SiteTitle
	a.Logo = fields.Logo
	a.Tagline = fields.Tagline
	a.BannerMessage = fields.BannerMessage
	a.AccentColor = fields.AccentColor
	a.DefaultTheme = fields.DefaultTheme
}
