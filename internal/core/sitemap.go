package core

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// Search-engine support: a sitemap of the publicly browsable surface and a
// matching robots.txt. Only boards a guest may read are listed, mirroring the
// web guest-browsing rules (ActorCanReadBoard with the guest principal), so the
// sitemap never leaks member-only or admin-hidden boards.

const (
	// DefaultSitemapInterval is the regeneration period when none is configured.
	DefaultSitemapInterval = 6 * time.Hour
	// sitemapMaxURLs keeps a single sitemap under the 50,000-URL protocol limit
	// with headroom; beyond this the remaining threads are dropped (logged by the
	// caller). A sitemap index can be added later if a deployment outgrows this.
	sitemapMaxURLs = 45000
	// sitemapThreadsPerBoard caps how many of each board's most-recently-active
	// threads are listed, so one busy board can't crowd out the rest.
	sitemapThreadsPerBoard = 2000
)

// guest principal used for sitemap visibility checks: empty id, "guest" role, so
// ActorCanReadBoard applies the GuestAccess override (default/hidden/public).
func sitemapGuest() *User { return &User{Role: "guest"} }

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

// SitemapStats reports what a generation pass produced (for logging/metrics).
type SitemapStats struct {
	Boards   int
	Threads  int
	Truncated bool
}

// GenerateSitemap builds an XML sitemap of the guest-readable site rooted at
// baseURL (e.g. "https://bbs.example.com"). Boards appear as /b/{id} and threads
// as /t/{id}; the homepage is always included. baseURL must be absolute for the
// sitemap to validate — a blank baseURL still produces relative-rooted entries,
// which the HTTP layer fills in from the request when no public URL is set.
func (c *Core) GenerateSitemap(baseURL string) ([]byte, SitemapStats, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	guest := sitemapGuest()
	set := sitemapURLSet{Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9"}
	set.URLs = append(set.URLs, sitemapURL{Loc: base + "/", ChangeFreq: "hourly"})

	boards, err := c.ListBoards()
	if err != nil {
		return nil, SitemapStats{}, err
	}

	var stats SitemapStats
	for _, b := range boards {
		if len(set.URLs) >= sitemapMaxURLs {
			stats.Truncated = true
			break
		}
		info, err := c.GetBoardInfo(b.ID)
		if err != nil || info == nil {
			continue
		}
		if !ActorCanReadBoard(guest, info) {
			continue
		}

		threads, err := c.ListThreads(b.ID, sitemapThreadsPerBoard, 0)
		if err != nil {
			continue
		}
		// Board lastmod = most recent thread activity (threads are last_seq DESC).
		boardEntry := sitemapURL{Loc: base + "/b/" + b.ID, ChangeFreq: "hourly"}
		if len(threads) > 0 {
			boardEntry.LastMod = sitemapTimestamp(threads[0].UpdatedAt)
		}
		set.URLs = append(set.URLs, boardEntry)
		stats.Boards++

		for _, t := range threads {
			if len(set.URLs) >= sitemapMaxURLs {
				stats.Truncated = true
				break
			}
			set.URLs = append(set.URLs, sitemapURL{
				Loc:        base + "/t/" + t.ID,
				LastMod:    sitemapTimestamp(t.UpdatedAt),
				ChangeFreq: "daily",
			})
			stats.Threads++
		}
	}

	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(set); err != nil {
		return nil, SitemapStats{}, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), stats, nil
}

// MinimalSitemap returns a valid sitemap containing only the homepage. Used as a
// safe fallback when a full generation pass fails (e.g. a transient DB error) so
// the endpoint always returns well-formed XML rather than an empty 200.
func MinimalSitemap(baseURL string) []byte {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	set := sitemapURLSet{
		Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9",
		URLs:  []sitemapURL{{Loc: base + "/", ChangeFreq: "hourly"}},
	}
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	_ = enc.Encode(set)
	buf.WriteByte('\n')
	return buf.Bytes()
}

// sitemapTimestamp formats a millisecond epoch as a W3C/RFC3339 datetime, or ""
// when the value is unset.
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
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
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
