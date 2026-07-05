package core

import (
	"github.com/juncoflockleader/budgie-bbs/internal/core/sitemodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/sitestore"
)

// SiteAppearance returns the current site appearance settings (sensible defaults
// if unset).
func (c *Core) SiteAppearance() (*sitemodel.Appearance, error) {
	return sitestore.Appearance(c.DB)
}

// SetSiteAppearance validates and persists the site appearance settings.
func (c *Core) SetSiteAppearance(a sitemodel.Appearance) (*sitemodel.Appearance, error) {
	fields, err := sitemodel.NormalizeAppearanceFields(a.Fields())
	if err != nil {
		return nil, err
	}
	normLayout, err := sitemodel.NormalizeTUIMainMenuLayout(a.MainMenuLayout)
	if err != nil {
		return nil, err
	}
	layoutRaw, err := sitemodel.EncodeTUIMainMenuLayout(normLayout)
	if err != nil {
		return nil, err
	}
	if err := sitestore.SetAppearance(c.DB, fields, layoutRaw, nowMS()); err != nil {
		return nil, err
	}
	return c.SiteAppearance()
}
