package sitestore

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/sitemodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/sqlstore"
)

func Appearance(db *sql.DB) (*sitemodel.Appearance, error) {
	defaults := sitemodel.DefaultAppearanceFields()
	out := &sitemodel.Appearance{SiteTitle: defaults.SiteTitle, DefaultTheme: defaults.DefaultTheme}
	var layoutRaw string
	err := sqlstore.QueryRow(db,
		`SELECT site_title, COALESCE(logo,''), tagline, banner_message, accent_color, default_theme,
		        COALESCE(tui_main_menu_layout,''), updated_at
		   FROM site_appearance_settings WHERE id='default'`,
	).Scan(&out.SiteTitle, &out.Logo, &out.Tagline, &out.BannerMessage, &out.AccentColor, &out.DefaultTheme, &layoutRaw, &out.UpdatedAt)
	if err == sql.ErrNoRows {
		layout := sitemodel.DefaultTUIMainMenuLayout()
		out.MainMenuLayout = &layout
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	layout := sitemodel.DecodeTUIMainMenuLayout(layoutRaw)
	out.MainMenuLayout = &layout
	out.ApplyFields(sitemodel.ApplyAppearanceDefaults(out.Fields()))
	return out, nil
}

func SetAppearance(db *sql.DB, fields sitemodel.AppearanceFields, layoutRaw string, updatedAt int64) error {
	_, err := sqlstore.Exec(db,
		`INSERT INTO site_appearance_settings (id, site_title, logo, tagline, banner_message, accent_color, default_theme, tui_main_menu_layout, updated_at)
		 VALUES ('default', ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   site_title=excluded.site_title, logo=excluded.logo, tagline=excluded.tagline, banner_message=excluded.banner_message,
		   accent_color=excluded.accent_color, default_theme=excluded.default_theme,
		   tui_main_menu_layout=excluded.tui_main_menu_layout, updated_at=excluded.updated_at`,
		fields.SiteTitle, fields.Logo, fields.Tagline, fields.BannerMessage, fields.AccentColor, fields.DefaultTheme, layoutRaw, updatedAt,
	)
	return err
}
