package sitemodel

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

const (
	// DefaultSitemapInterval is the regeneration period when none is configured.
	DefaultSitemapInterval = 6 * time.Hour
	// SitemapMaxURLs keeps a single sitemap under the 50,000-URL protocol limit
	// with headroom; beyond this the remaining threads are dropped.
	SitemapMaxURLs = 45000
	// SitemapThreadsPerBoard caps how many of each board's most-recently-active
	// threads are listed, so one busy board can't crowd out the rest.
	SitemapThreadsPerBoard = 2000
)

// SitemapStats reports what a generation pass produced.
type SitemapStats struct {
	Boards    int
	Threads   int
	Truncated bool
}

type SitemapEntry struct {
	Path       string
	LastModMS  int64
	ChangeFreq string
}

type sitemapURL struct {
	XMLName    xml.Name `xml:"url"`
	Loc        string   `xml:"loc"`
	LastMod    string   `xml:"lastmod,omitempty"`
	ChangeFreq string   `xml:"changefreq,omitempty"`
}

type sitemapURLSet struct {
	XMLName xml.Name `xml:"urlset"`
	Xmlns   string   `xml:"xmlns,attr"`
	URLs    []sitemapURL
}

// BuildSitemap returns a valid XML sitemap rooted at baseURL. The homepage is
// always included, followed by the provided relative-path entries.
func BuildSitemap(baseURL string, entries []SitemapEntry) ([]byte, error) {
	base := normalizeSitemapBaseURL(baseURL)
	set := sitemapURLSet{
		Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9",
		URLs:  make([]sitemapURL, 0, len(entries)+1),
	}
	set.URLs = append(set.URLs, sitemapURL{Loc: base + "/", ChangeFreq: "hourly"})
	for _, entry := range entries {
		set.URLs = append(set.URLs, sitemapURL{
			Loc:        base + relativeSitemapPath(entry.Path),
			LastMod:    sitemapTimestamp(entry.LastModMS),
			ChangeFreq: entry.ChangeFreq,
		})
	}
	return encodeSitemap(set)
}

// MinimalSitemap returns a valid sitemap containing only the homepage.
func MinimalSitemap(baseURL string) []byte {
	data, _ := BuildSitemap(baseURL, nil)
	return data
}

func encodeSitemap(set sitemapURLSet) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(set); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

func normalizeSitemapBaseURL(baseURL string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

func relativeSitemapPath(path string) string {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "/") {
		return path
	}
	return "/" + path
}

func sitemapTimestamp(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// GenerateRobotsTxt returns a robots.txt that allows crawling of the public site
// while keeping the JSON API out of the index, and advertises the sitemap when a
// base URL is known.
func GenerateRobotsTxt(baseURL string) []byte {
	base := normalizeSitemapBaseURL(baseURL)
	var b strings.Builder
	b.WriteString("User-agent: *\n")
	b.WriteString("Allow: /\n")
	// The API serves JSON (and personal/member endpoints 401 for crawlers); keep
	// it out of the index. Public content is reachable via the SPA routes.
	b.WriteString("Disallow: /api/\n")
	if base != "" {
		fmt.Fprintf(&b, "\nSitemap: %s/sitemap.xml\n", base)
	}
	return []byte(b.String())
}
