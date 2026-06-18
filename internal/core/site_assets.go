package core

import (
	"database/sql"
	"errors"
	"strings"
)

// validSiteAssets are the admin-uploadable site images, served publicly at
// GET /api/v1/site/asset/<name>.
var validSiteAssets = map[string]bool{"logo": true, "banner": true}

// ValidSiteAsset reports whether name is an uploadable site asset slot.
func ValidSiteAsset(name string) bool { return validSiteAssets[strings.TrimSpace(name)] }

// SetSiteAsset stores (or replaces) an uploaded site image.
func (c *Core) SetSiteAsset(name, contentType string, data []byte) error {
	name = strings.TrimSpace(name)
	if !validSiteAssets[name] {
		return errors.New("unknown site asset")
	}
	if len(data) == 0 {
		return errors.New("empty asset")
	}
	_, err := qExec(c.DB,
		`INSERT INTO site_assets (name, content_type, data, byte_size, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
		   content_type=excluded.content_type, data=excluded.data,
		   byte_size=excluded.byte_size, updated_at=excluded.updated_at`,
		name, contentType, data, len(data), nowMS())
	return err
}

// SiteAsset returns the stored bytes for an asset, or sql.ErrNoRows if unset.
func (c *Core) SiteAsset(name string) (data []byte, contentType string, updatedAt int64, err error) {
	name = strings.TrimSpace(name)
	if !validSiteAssets[name] {
		return nil, "", 0, sql.ErrNoRows
	}
	err = qQueryRow(c.DB, `SELECT data, content_type, updated_at FROM site_assets WHERE name=?`, name).
		Scan(&data, &contentType, &updatedAt)
	return data, contentType, updatedAt, err
}

// DeleteSiteAsset clears an uploaded asset (reverting to the glyph/no-banner).
func (c *Core) DeleteSiteAsset(name string) error {
	_, err := qExec(c.DB, `DELETE FROM site_assets WHERE name=?`, strings.TrimSpace(name))
	return err
}
