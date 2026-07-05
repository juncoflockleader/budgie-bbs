package sitemodel

import (
	"strings"
	"testing"
	"time"
)

func TestBuildSitemap(t *testing.T) {
	data, err := BuildSitemap(" https://bbs.example.com/ ", []SitemapEntry{
		{Path: "b/general", LastModMS: time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC).UnixMilli(), ChangeFreq: "hourly"},
		{Path: "/t/thread1", ChangeFreq: "daily"},
	})
	if err != nil {
		t.Fatalf("BuildSitemap: %v", err)
	}
	body := string(data)
	for _, want := range []string{
		`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`,
		"<loc>https://bbs.example.com/</loc>",
		"<loc>https://bbs.example.com/b/general</loc>",
		"<lastmod>2026-07-04T12:00:00Z</lastmod>",
		"<loc>https://bbs.example.com/t/thread1</loc>",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("sitemap missing %q\n%s", want, body)
		}
	}
}

func TestGenerateRobotsTxt(t *testing.T) {
	body := string(GenerateRobotsTxt(" https://bbs.example.com/ "))
	for _, want := range []string{"User-agent: *", "Disallow: /api/", "Sitemap: https://bbs.example.com/sitemap.xml"} {
		if !strings.Contains(body, want) {
			t.Fatalf("robots.txt missing %q\n%s", want, body)
		}
	}
}
