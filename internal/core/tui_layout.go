package core

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// TUIBlock is one stacked element of the SSH/TUI main-menu layout. Blocks are
// rendered top to bottom; each is aligned within the panel width. This favours a
// robust stacked-and-aligned model over fragile absolute x/y positioning so the
// layout holds up across terminal sizes.
type TUIBlock struct {
	Type  string `json:"type"`            // art | text | menu | spacer
	Align string `json:"align,omitempty"` // left | center | right (default left)
	// Art carries inline ASCII/ANSI art for Type=="art". If Stock names a known
	// stock art, it takes precedence over Art.
	Art   string `json:"art,omitempty"`
	Stock string `json:"stock,omitempty"`
	Text  string `json:"text,omitempty"`  // for Type=="text"
	Color string `json:"color,omitempty"` // optional #rgb/#rrggbb for art/text
	Lines int    `json:"lines,omitempty"` // blank line count for Type=="spacer"
}

// TUIMainMenuLayout is the admin-configurable main-menu composition for the SSH
// TUI. A nil/empty layout renders the built-in default (defaultTUIMainMenuLayout).
type TUIMainMenuLayout struct {
	Blocks []TUIBlock `json:"blocks"`
}

const (
	tuiBlockArt    = "art"
	tuiBlockText   = "text"
	tuiBlockMenu   = "menu"
	tuiBlockSpacer = "spacer"

	tuiAlignLeft   = "left"
	tuiAlignCenter = "center"
	tuiAlignRight  = "right"

	// maxTUILayoutBlocks and maxTUIArtBytes bound stored layout size so a single
	// config row can't grow unbounded or wedge the renderer.
	maxTUILayoutBlocks = 24
	maxTUIArtBytes     = 8 << 10 // 8 KiB per inline art block
)

var tuiBlockTypes = map[string]bool{
	tuiBlockArt: true, tuiBlockText: true, tuiBlockMenu: true, tuiBlockSpacer: true,
}

var tuiAligns = map[string]bool{
	tuiAlignLeft: true, tuiAlignCenter: true, tuiAlignRight: true,
}

// defaultTUIMainMenuLayout is used when an admin has not configured one. It
// centers the wordmark banner over a tagline and the menu, so a fresh install
// looks composed rather than bare.
func defaultTUIMainMenuLayout() TUIMainMenuLayout {
	return TUIMainMenuLayout{Blocks: []TUIBlock{
		{Type: tuiBlockArt, Stock: "budgie-bbs", Align: tuiAlignCenter},
		{Type: tuiBlockSpacer, Lines: 1},
		{Type: tuiBlockText, Text: "", Align: tuiAlignCenter}, // tagline filled at render time
		{Type: tuiBlockSpacer, Lines: 1},
		{Type: tuiBlockMenu, Align: tuiAlignLeft},
	}}
}

// normalizeTUIMainMenuLayout validates and canonicalizes a layout. It enforces a
// single menu block (the menu must appear exactly once), known types/aligns, and
// size bounds. An empty layout returns the default.
func normalizeTUIMainMenuLayout(in *TUIMainMenuLayout) (TUIMainMenuLayout, error) {
	if in == nil || len(in.Blocks) == 0 {
		return defaultTUIMainMenuLayout(), nil
	}
	if len(in.Blocks) > maxTUILayoutBlocks {
		return TUIMainMenuLayout{}, errors.New("too many layout blocks")
	}
	out := TUIMainMenuLayout{Blocks: make([]TUIBlock, 0, len(in.Blocks))}
	menuCount := 0
	for _, b := range in.Blocks {
		b.Type = strings.ToLower(strings.TrimSpace(b.Type))
		if !tuiBlockTypes[b.Type] {
			return TUIMainMenuLayout{}, errors.New("unknown layout block type: " + b.Type)
		}
		b.Align = strings.ToLower(strings.TrimSpace(b.Align))
		if b.Align == "" {
			b.Align = tuiAlignLeft
		}
		if !tuiAligns[b.Align] {
			return TUIMainMenuLayout{}, errors.New("unknown alignment: " + b.Align)
		}
		b.Color = strings.ToLower(strings.TrimSpace(b.Color))
		if b.Color != "" && !hexColorRe.MatchString(b.Color) {
			return TUIMainMenuLayout{}, errors.New("block color must be a hex value like #58a6ff")
		}
		switch b.Type {
		case tuiBlockArt:
			b.Stock = strings.TrimSpace(b.Stock)
			if b.Stock != "" && !stockTUIArtExists(b.Stock) {
				return TUIMainMenuLayout{}, errors.New("unknown stock art: " + b.Stock)
			}
			if b.Stock == "" && len(b.Art) > maxTUIArtBytes {
				return TUIMainMenuLayout{}, errors.New("art block exceeds size limit")
			}
			if b.Stock == "" && strings.TrimSpace(b.Art) == "" {
				continue // drop empty art blocks
			}
		case tuiBlockText:
			if len(b.Text) > 500 {
				return TUIMainMenuLayout{}, errors.New("text block must be 500 characters or less")
			}
		case tuiBlockMenu:
			menuCount++
		case tuiBlockSpacer:
			if b.Lines <= 0 {
				b.Lines = 1
			}
			if b.Lines > 12 {
				b.Lines = 12
			}
		}
		out.Blocks = append(out.Blocks, b)
	}
	if menuCount == 0 {
		// The menu must always be reachable; append it if the admin omitted it.
		out.Blocks = append(out.Blocks, TUIBlock{Type: tuiBlockMenu, Align: tuiAlignLeft})
	} else if menuCount > 1 {
		return TUIMainMenuLayout{}, errors.New("layout must contain the menu block at most once")
	}
	return out, nil
}

// --- Stock ASCII art catalog ---------------------------------------------

// stockTUIArt maps a stable key to ready-made ASCII art. Kept intentionally
// monochrome-friendly (plain ASCII) so blocks render on any terminal; admins can
// tint a block via TUIBlock.Color.
var stockTUIArt = map[string]string{
	"budgie-bbs": "" +
		` ____  _   _ ____   ____ ___ _____   ____  ____  ____ ` + "\n" +
		`| __ )| | | |  _ \ / ___|_ _| ____| | __ )| __ )/ ___|` + "\n" +
		`|  _ \| | | | | | | |  _ | ||  _|   |  _ \|  _ \___ \ ` + "\n" +
		`| |_) | |_| | |_| | |_| || || |___  | |_) | |_) |___) |` + "\n" +
		`|____/ \___/|____/ \____|___|_____| |____/|____/|____/`,
	"budgie": "" +
		`   .-.` + "\n" +
		`  (o o)   chirp!` + "\n" +
		`  | O \` + "\n" +
		`   \   \` + "\n" +
		`    ` + "`" + `~~~'`,
	"divider": `════════════════════ ◆ ════════════════════`,
	"welcome": "" +
		`╔══════════════════════════════════╗` + "\n" +
		`║      W E L C O M E   A B O A R D    ║` + "\n" +
		`╚══════════════════════════════════╝`,
}

// DefaultTUIMainMenuLayout exposes the built-in main-menu layout for callers
// (e.g. the TUI) that need a fallback when none is configured.
func DefaultTUIMainMenuLayout() TUIMainMenuLayout { return defaultTUIMainMenuLayout() }

func stockTUIArtExists(name string) bool {
	_, ok := stockTUIArt[strings.TrimSpace(name)]
	return ok
}

// StockTUIArt returns the art body for a stock key, or "" if unknown.
func StockTUIArt(name string) string {
	return stockTUIArt[strings.TrimSpace(name)]
}

// StockTUIArtNames returns the sorted list of available stock art keys (used by
// the admin UI to offer presets).
func StockTUIArtNames() []string {
	names := make([]string, 0, len(stockTUIArt))
	for name := range stockTUIArt {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// encode/decode helpers for the JSON-backed storage column.

func encodeTUIMainMenuLayout(l TUIMainMenuLayout) (string, error) {
	if len(l.Blocks) == 0 {
		return "", nil
	}
	b, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func decodeTUIMainMenuLayout(raw string) TUIMainMenuLayout {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultTUIMainMenuLayout()
	}
	var l TUIMainMenuLayout
	if err := json.Unmarshal([]byte(raw), &l); err != nil || len(l.Blocks) == 0 {
		return defaultTUIMainMenuLayout()
	}
	return l
}
