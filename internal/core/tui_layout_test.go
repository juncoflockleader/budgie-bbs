package core

import (
	"strings"
	"testing"
)

func TestNormalizeTUIMainMenuLayoutDefaults(t *testing.T) {
	got, err := normalizeTUIMainMenuLayout(nil)
	if err != nil {
		t.Fatalf("nil layout: %v", err)
	}
	if len(got.Blocks) == 0 {
		t.Fatal("nil layout should yield the default, got empty")
	}
	menus := 0
	for _, b := range got.Blocks {
		if b.Type == tuiBlockMenu {
			menus++
		}
	}
	if menus != 1 {
		t.Fatalf("default layout must contain exactly one menu block, got %d", menus)
	}
}

func TestNormalizeTUIMainMenuLayoutValidation(t *testing.T) {
	// Unknown block type rejected.
	if _, err := normalizeTUIMainMenuLayout(&TUIMainMenuLayout{Blocks: []TUIBlock{{Type: "bogus"}}}); err == nil {
		t.Fatal("expected error for unknown block type")
	}
	// Unknown stock art rejected.
	if _, err := normalizeTUIMainMenuLayout(&TUIMainMenuLayout{Blocks: []TUIBlock{{Type: "art", Stock: "nope"}}}); err == nil {
		t.Fatal("expected error for unknown stock art")
	}
	// Bad color rejected.
	if _, err := normalizeTUIMainMenuLayout(&TUIMainMenuLayout{Blocks: []TUIBlock{{Type: "text", Text: "hi", Color: "blue"}}}); err == nil {
		t.Fatal("expected error for non-hex color")
	}
	// Two menu blocks rejected.
	if _, err := normalizeTUIMainMenuLayout(&TUIMainMenuLayout{Blocks: []TUIBlock{{Type: "menu"}, {Type: "menu"}}}); err == nil {
		t.Fatal("expected error for duplicate menu block")
	}
	// Menu auto-appended when omitted.
	got, err := normalizeTUIMainMenuLayout(&TUIMainMenuLayout{Blocks: []TUIBlock{{Type: "art", Stock: "budgie-bbs", Align: "center"}}})
	if err != nil {
		t.Fatalf("art-only layout: %v", err)
	}
	last := got.Blocks[len(got.Blocks)-1]
	if last.Type != tuiBlockMenu {
		t.Fatalf("menu should be auto-appended, last block is %q", last.Type)
	}
	// Empty inline art dropped.
	got, err = normalizeTUIMainMenuLayout(&TUIMainMenuLayout{Blocks: []TUIBlock{{Type: "art", Art: "   "}, {Type: "menu"}}})
	if err != nil {
		t.Fatalf("empty art layout: %v", err)
	}
	for _, b := range got.Blocks {
		if b.Type == tuiBlockArt {
			t.Fatal("empty art block should have been dropped")
		}
	}
}

func TestTUIMainMenuLayoutEncodeDecodeRoundTrip(t *testing.T) {
	in := TUIMainMenuLayout{Blocks: []TUIBlock{
		{Type: "art", Stock: "budgie-bbs", Align: "center"},
		{Type: "spacer", Lines: 2},
		{Type: "text", Text: "welcome", Align: "center", Color: "#58a6ff"},
		{Type: "menu", Align: "left"},
	}}
	raw, err := encodeTUIMainMenuLayout(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out := decodeTUIMainMenuLayout(raw)
	if len(out.Blocks) != len(in.Blocks) {
		t.Fatalf("round-trip block count: got %d want %d", len(out.Blocks), len(in.Blocks))
	}
	if out.Blocks[2].Color != "#58a6ff" || out.Blocks[2].Text != "welcome" {
		t.Fatalf("round-trip lost text block fields: %+v", out.Blocks[2])
	}
	// Empty/blank decodes to the default.
	if d := decodeTUIMainMenuLayout(""); len(d.Blocks) == 0 {
		t.Fatal("empty raw should decode to default")
	}
}

func TestStockTUIArtCatalog(t *testing.T) {
	names := StockTUIArtNames()
	if len(names) == 0 {
		t.Fatal("expected stock art catalog to be non-empty")
	}
	for _, n := range names {
		if strings.TrimSpace(StockTUIArt(n)) == "" {
			t.Fatalf("stock art %q is empty", n)
		}
		if !stockTUIArtExists(n) {
			t.Fatalf("stock art %q should exist", n)
		}
	}
	if StockTUIArt("definitely-not-real") != "" {
		t.Fatal("unknown stock art should return empty")
	}
}
