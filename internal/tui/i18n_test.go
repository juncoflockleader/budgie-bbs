package tui

import (
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
)

func TestLocaleFromEnviron(t *testing.T) {
	cases := []struct {
		env  []string
		want localeCode
	}{
		{[]string{"BUDGIE_LANG=zh-CN"}, localeZHCN},
		{[]string{"BUDGIE_LANG=zh-TW"}, localeZHTW},
		{[]string{"BUDGIE_LANG=zh_TW.UTF-8"}, localeZHTW},
		{[]string{"LC_ALL=zh_CN.UTF-8"}, localeZHCN},
		{[]string{"LC_ALL=zh_TW.UTF-8"}, localeZHTW},
		{[]string{"LC_MESSAGES=zh_CN.UTF-8"}, localeZHCN},
		{[]string{"LC_MESSAGES=zh_TW.UTF-8"}, localeZHTW},
		{[]string{"LANG=zh_CN.UTF-8"}, localeZHCN},
		{[]string{"LANG=zh-Hant"}, localeZHTW},
		{[]string{"LANG=en_US.UTF-8"}, localeEN},
		{[]string{"LANG=C"}, localeEN},
		{[]string{"LANG=bad"}, localeEN},
	}

	for _, tc := range cases {
		if got := localeFromEnviron(tc.env); got != tc.want {
			t.Fatalf("localeFromEnviron(%v) = %s, want %s", tc.env, got, tc.want)
		}
	}
}

func TestTUITranslationFallback(t *testing.T) {
	got := trLocale(localeCode("es"), msgTitleMainMenu, map[string]string{"status": "ok"})
	if got == string(msgTitleMainMenu) {
		t.Fatalf("expected fallback translation for msgTitleMainMenu")
	}

	got = trLocale(localeCode("es"), msgStatusError, map[string]string{"message": "x"})
	if got != "error: x" {
		t.Fatalf("expected fallback translation with interpolation, got: %q", got)
	}
}

func TestTUIChineseHeaderDoesNotWrap(t *testing.T) {
	m := model{
		actor:        &core.User{Name: "alice"},
		page:         pageMainMenu,
		width:        40,
		height:       20,
		supportsANSI: true,
		locale:       localeZHTW,
	}
	m.list = list.New(nil, newBBSListDelegate(nil), 40, sectionContentHeightFor(m.height))
	m.rebuildList()

	headerLine := strings.SplitN(m.View(), "\n", 2)[0]
	if lipgloss.Width(headerLine) > m.width {
		t.Fatalf("expected first header line not to wrap at width %d, got width %d", m.width, lipgloss.Width(headerLine))
	}
}
