package core

import "github.com/juncoflockleader/budgie-bbs/internal/core/sitemodel"

type TUIBlock = sitemodel.TUIBlock
type TUIMainMenuLayout = sitemodel.TUIMainMenuLayout

// DefaultTUIMainMenuLayout exposes the built-in main-menu layout for callers
// (e.g. the TUI) that need a fallback when none is configured.
func DefaultTUIMainMenuLayout() TUIMainMenuLayout {
	return sitemodel.DefaultTUIMainMenuLayout()
}

// StockTUIArt returns the art body for a stock key, or "" if unknown.
func StockTUIArt(name string) string {
	return sitemodel.StockTUIArt(name)
}

// StockTUIArtNames returns the sorted list of available stock art keys (used by
// the admin UI to offer presets).
func StockTUIArtNames() []string {
	return sitemodel.StockTUIArtNames()
}
