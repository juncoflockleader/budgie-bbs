package core

import (
	"database/sql"
	"errors"
	"regexp"
	"strings"
)

// SiteAppearance holds admin-configurable site-wide branding/appearance. All
// fields are public (served to anonymous users for the login page and header).
type SiteAppearance struct {
	SiteTitle     string `json:"siteTitle"`
	Tagline       string `json:"tagline"`
	BannerMessage string `json:"bannerMessage"`
	AccentColor   string `json:"accentColor"`  // "" or #rgb/#rrggbb
	DefaultTheme  string `json:"defaultTheme"` // dark|dim|light|warm
	// MainMenuLayout is the admin-configurable SSH/TUI main-menu composition.
	// Nil means "use the built-in default layout".
	MainMenuLayout *TUIMainMenuLayout `json:"mainMenuLayout,omitempty"`
	UpdatedAt      int64              `json:"updatedAt"`
}

const defaultSiteTitle = "Budgie BBS"

var siteThemes = map[string]bool{"dark": true, "dim": true, "light": true, "warm": true}

var hexColorRe = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// SiteAppearance returns the current site appearance settings (sensible defaults
// if unset).
func (c *Core) SiteAppearance() (*SiteAppearance, error) {
	out := &SiteAppearance{SiteTitle: defaultSiteTitle, DefaultTheme: "dark"}
	var layoutRaw string
	err := qQueryRow(c.DB,
		`SELECT site_title, tagline, banner_message, accent_color, default_theme,
		        COALESCE(tui_main_menu_layout,''), updated_at
		   FROM site_appearance_settings WHERE id='default'`,
	).Scan(&out.SiteTitle, &out.Tagline, &out.BannerMessage, &out.AccentColor, &out.DefaultTheme, &layoutRaw, &out.UpdatedAt)
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
	if strings.TrimSpace(out.SiteTitle) == "" {
		out.SiteTitle = defaultSiteTitle
	}
	if !siteThemes[out.DefaultTheme] {
		out.DefaultTheme = "dark"
	}
	return out, nil
}

// SetSiteAppearance validates and persists the site appearance settings.
func (c *Core) SetSiteAppearance(a SiteAppearance) (*SiteAppearance, error) {
	title := strings.TrimSpace(a.SiteTitle)
	if title == "" {
		title = defaultSiteTitle
	}
	if len(title) > 80 {
		return nil, errors.New("site title must be 80 characters or less")
	}
	tagline := strings.TrimSpace(a.Tagline)
	if len(tagline) > 200 {
		return nil, errors.New("tagline must be 200 characters or less")
	}
	banner := strings.TrimSpace(a.BannerMessage)
	if len(banner) > 500 {
		return nil, errors.New("banner must be 500 characters or less")
	}
	accent := strings.ToLower(strings.TrimSpace(a.AccentColor))
	if accent != "" && !hexColorRe.MatchString(accent) {
		return nil, errors.New("accent color must be a hex value like #58a6ff")
	}
	theme := strings.TrimSpace(a.DefaultTheme)
	if theme == "" {
		theme = "dark"
	}
	if !siteThemes[theme] {
		return nil, errors.New("default theme must be one of dark, dim, light, warm")
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
		`INSERT INTO site_appearance_settings (id, site_title, tagline, banner_message, accent_color, default_theme, tui_main_menu_layout, updated_at)
		 VALUES ('default', ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   site_title=excluded.site_title, tagline=excluded.tagline, banner_message=excluded.banner_message,
		   accent_color=excluded.accent_color, default_theme=excluded.default_theme,
		   tui_main_menu_layout=excluded.tui_main_menu_layout, updated_at=excluded.updated_at`,
		title, tagline, banner, accent, theme, layoutRaw, nowMS(),
	); err != nil {
		return nil, err
	}
	return c.SiteAppearance()
}
