package core

import (
	"context"
	"database/sql"
	"errors"

	"github.com/juncoflockleader/budgie-bbs/internal/assetstore"
	"github.com/juncoflockleader/budgie-bbs/internal/core/sitemodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/sitestore"
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
		oldVersion := sitestore.AssetVersion(c.DB, name)
		if err := c.assetStore.Put(context.Background(), sitemodel.AssetObjectKey(name, version), contentType, data); err != nil {
			return err
		}
		if err := sitestore.StoreExternalAsset(c.DB, name, contentType, len(data), version); err != nil {
			return err
		}
		if oldVersion > 0 && oldVersion != version {
			_ = c.assetStore.Delete(context.Background(), sitemodel.AssetObjectKey(name, oldVersion)) // best effort
		}
		return nil
	}

	return sitestore.StoreInlineAsset(c.DB, name, contentType, data, version)
}

// SiteAsset returns an asset's bytes for the app-served path, or sql.ErrNoRows
// if unset. When bytes live in the external store they are fetched from it.
func (c *Core) SiteAsset(name string) (data []byte, contentType string, updatedAt int64, err error) {
	name = sitemodel.NormalizeAssetName(name)
	if !sitemodel.ValidAsset(name) {
		return nil, "", 0, sql.ErrNoRows
	}
	stored, err := sitestore.LoadAsset(c.DB, name)
	if err != nil {
		return nil, "", 0, err
	}
	if len(stored.Data) > 0 {
		return stored.Data, stored.ContentType, stored.UpdatedAt, nil
	}
	if c.assetStore != nil {
		b, ct, gerr := c.assetStore.Get(context.Background(), sitemodel.AssetObjectKey(name, stored.UpdatedAt))
		if errors.Is(gerr, assetstore.ErrNotFound) {
			return nil, "", 0, sql.ErrNoRows
		}
		if gerr != nil {
			return nil, "", 0, gerr
		}
		if ct == "" {
			ct = stored.ContentType
		}
		return b, ct, stored.UpdatedAt, nil
	}
	return nil, "", 0, sql.ErrNoRows
}

// DeleteSiteAsset clears an uploaded asset (reverting to the glyph/no-banner).
func (c *Core) DeleteSiteAsset(name string) error {
	name = sitemodel.NormalizeAssetName(name)
	version := sitestore.AssetVersion(c.DB, name)
	if c.assetStore != nil && version > 0 {
		_ = c.assetStore.Delete(context.Background(), sitemodel.AssetObjectKey(name, version)) // best effort
	}
	return sitestore.DeleteAsset(c.DB, name)
}

// SiteAssetVersions returns each asset slot's version (updated_at ms), 0 when
// unset. Surfaced to the web so it can build cache-busting / CDN URLs.
func (c *Core) SiteAssetVersions() map[string]int64 {
	return sitestore.AssetVersions(c.DB)
}
