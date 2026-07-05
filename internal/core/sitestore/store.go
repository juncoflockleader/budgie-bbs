package sitestore

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/sitemodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/sqlstore"
)

type AssetRecord struct {
	Data        []byte
	ContentType string
	UpdatedAt   int64
}

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

func AssetVersion(db *sql.DB, name string) int64 {
	var version int64
	_ = sqlstore.QueryRow(db, `SELECT updated_at FROM site_assets WHERE name=?`, name).Scan(&version)
	return version
}

func StoreExternalAsset(db *sql.DB, name, contentType string, byteSize int, updatedAt int64) error {
	_, err := sqlstore.Exec(db,
		`INSERT INTO site_assets (name, content_type, data, byte_size, updated_at)
		 VALUES (?, ?, NULL, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
		   content_type=excluded.content_type, data=NULL,
		   byte_size=excluded.byte_size, updated_at=excluded.updated_at`,
		name, contentType, byteSize, updatedAt,
	)
	return err
}

func StoreInlineAsset(db *sql.DB, name, contentType string, data []byte, updatedAt int64) error {
	_, err := sqlstore.Exec(db,
		`INSERT INTO site_assets (name, content_type, data, byte_size, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
		   content_type=excluded.content_type, data=excluded.data,
		   byte_size=excluded.byte_size, updated_at=excluded.updated_at`,
		name, contentType, data, len(data), updatedAt,
	)
	return err
}

func LoadAsset(db *sql.DB, name string) (AssetRecord, error) {
	var out AssetRecord
	err := sqlstore.QueryRow(db, `SELECT data, content_type, updated_at FROM site_assets WHERE name=?`, name).
		Scan(&out.Data, &out.ContentType, &out.UpdatedAt)
	return out, err
}

func DeleteAsset(db *sql.DB, name string) error {
	_, err := sqlstore.Exec(db, `DELETE FROM site_assets WHERE name=?`, name)
	return err
}

func AssetVersions(db *sql.DB) map[string]int64 {
	out := sitemodel.EmptyAssetVersions()
	rows, err := sqlstore.Query(db, `SELECT name, updated_at FROM site_assets`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var version int64
		if rows.Scan(&name, &version) == nil {
			name = sitemodel.NormalizeAssetName(name)
			if sitemodel.ValidAsset(name) {
				out[name] = version
			}
		}
	}
	return out
}
