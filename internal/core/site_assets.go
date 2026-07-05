package core

import (
	"context"
	"database/sql"
	"errors"

	"github.com/juncoflockleader/budgie-bbs/internal/assetstore"
	"github.com/juncoflockleader/budgie-bbs/internal/core/sitemodel"
)

// SetAssetStore installs an external object store (e.g. S3/R2) for site-asset
// bytes. Call once at startup before serving; nil keeps bytes in the DB.
func (c *Core) SetAssetStore(s assetstore.Store) { c.assetStore = s }

// AssetPublicBaseURL returns the external base URL clients/CDN use to read site
// assets, or "" when the app serves them itself.
func (c *Core) AssetPublicBaseURL() string {
	if c.assetStore == nil {
		return ""
	}
	return c.assetStore.PublicBaseURL()
}

// SetSiteAsset stores (or replaces) an uploaded site image. Bytes go to the
// external store under a versioned key when configured, else into the DB.
func (c *Core) SetSiteAsset(name, contentType string, data []byte) error {
	name = sitemodel.NormalizeAssetName(name)
	if !sitemodel.ValidAsset(name) {
		return errors.New("unknown site asset")
	}
	if len(data) == 0 {
		return errors.New("empty asset")
	}
	version := nowMS()

	if c.assetStore != nil {
		var oldVersion int64
		_ = qQueryRow(c.DB, `SELECT updated_at FROM site_assets WHERE name=?`, name).Scan(&oldVersion)
		if err := c.assetStore.Put(context.Background(), sitemodel.AssetObjectKey(name, version), contentType, data); err != nil {
			return err
		}
		if _, err := qExec(c.DB,
			`INSERT INTO site_assets (name, content_type, data, byte_size, updated_at)
			 VALUES (?, ?, NULL, ?, ?)
			 ON CONFLICT(name) DO UPDATE SET
			   content_type=excluded.content_type, data=NULL,
			   byte_size=excluded.byte_size, updated_at=excluded.updated_at`,
			name, contentType, len(data), version); err != nil {
			return err
		}
		if oldVersion > 0 && oldVersion != version {
			_ = c.assetStore.Delete(context.Background(), sitemodel.AssetObjectKey(name, oldVersion)) // best effort
		}
		return nil
	}

	_, err := qExec(c.DB,
		`INSERT INTO site_assets (name, content_type, data, byte_size, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
		   content_type=excluded.content_type, data=excluded.data,
		   byte_size=excluded.byte_size, updated_at=excluded.updated_at`,
		name, contentType, data, len(data), version)
	return err
}

// SiteAsset returns an asset's bytes for the app-served path, or sql.ErrNoRows
// if unset. When bytes live in the external store they are fetched from it.
func (c *Core) SiteAsset(name string) (data []byte, contentType string, updatedAt int64, err error) {
	name = sitemodel.NormalizeAssetName(name)
	if !sitemodel.ValidAsset(name) {
		return nil, "", 0, sql.ErrNoRows
	}
	var raw []byte
	err = qQueryRow(c.DB, `SELECT data, content_type, updated_at FROM site_assets WHERE name=?`, name).
		Scan(&raw, &contentType, &updatedAt)
	if err != nil {
		return nil, "", 0, err
	}
	if len(raw) > 0 {
		return raw, contentType, updatedAt, nil
	}
	if c.assetStore != nil {
		b, ct, gerr := c.assetStore.Get(context.Background(), sitemodel.AssetObjectKey(name, updatedAt))
		if errors.Is(gerr, assetstore.ErrNotFound) {
			return nil, "", 0, sql.ErrNoRows
		}
		if gerr != nil {
			return nil, "", 0, gerr
		}
		if ct == "" {
			ct = contentType
		}
		return b, ct, updatedAt, nil
	}
	return nil, "", 0, sql.ErrNoRows
}

// DeleteSiteAsset clears an uploaded asset (reverting to the glyph/no-banner).
func (c *Core) DeleteSiteAsset(name string) error {
	name = sitemodel.NormalizeAssetName(name)
	var version int64
	_ = qQueryRow(c.DB, `SELECT updated_at FROM site_assets WHERE name=?`, name).Scan(&version)
	if c.assetStore != nil && version > 0 {
		_ = c.assetStore.Delete(context.Background(), sitemodel.AssetObjectKey(name, version)) // best effort
	}
	_, err := qExec(c.DB, `DELETE FROM site_assets WHERE name=?`, name)
	return err
}

// SiteAssetVersions returns each asset slot's version (updated_at ms), 0 when
// unset. Surfaced to the web so it can build cache-busting / CDN URLs.
func (c *Core) SiteAssetVersions() map[string]int64 {
	out := sitemodel.EmptyAssetVersions()
	rows, err := c.DB.Query(`SELECT name, updated_at FROM site_assets`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		var v int64
		if rows.Scan(&n, &v) == nil {
			name := sitemodel.NormalizeAssetName(n)
			if sitemodel.ValidAsset(name) {
				out[name] = v
			}
		}
	}
	return out
}
