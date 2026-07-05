package core

import "github.com/juncoflockleader/budgie-bbs/internal/core/sitemodel"

// Search-engine support: a sitemap of the publicly browsable surface and a
// matching robots.txt. Only boards a guest may read are listed, mirroring the
// web guest-browsing rules (ActorCanReadBoard with the guest principal), so the
// sitemap never leaks member-only or admin-hidden boards.

const DefaultSitemapInterval = sitemodel.DefaultSitemapInterval

// guest principal used for sitemap visibility checks: empty id, "guest" role, so
// ActorCanReadBoard applies the GuestAccess override (default/hidden/public).
func sitemapGuest() *User { return &User{Role: "guest"} }

// GenerateSitemap builds an XML sitemap of the guest-readable site rooted at
// baseURL (e.g. "https://bbs.example.com"). Boards appear as /b/{id} and threads
// as /t/{id}; the homepage is always included. baseURL must be absolute for the
// sitemap to validate — a blank baseURL still produces relative-rooted entries,
// which the HTTP layer fills in from the request when no public URL is set.
func (c *Core) GenerateSitemap(baseURL string) ([]byte, sitemodel.SitemapStats, error) {
	guest := sitemapGuest()
	boards, err := c.ListBoards()
	if err != nil {
		return nil, sitemodel.SitemapStats{}, err
	}

	var stats sitemodel.SitemapStats
	entries := make([]sitemodel.SitemapEntry, 0)
	for _, b := range boards {
		if len(entries)+1 >= sitemodel.SitemapMaxURLs {
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

		threads, err := c.ListThreads(b.ID, sitemodel.SitemapThreadsPerBoard, 0)
		if err != nil {
			continue
		}
		// Board lastmod = most recent thread activity (threads are last_seq DESC).
		boardEntry := sitemodel.SitemapEntry{Path: "/b/" + b.ID, ChangeFreq: "hourly"}
		if len(threads) > 0 {
			boardEntry.LastModMS = threads[0].UpdatedAt
		}
		entries = append(entries, boardEntry)
		stats.Boards++

		for _, t := range threads {
			if len(entries)+1 >= sitemodel.SitemapMaxURLs {
				stats.Truncated = true
				break
			}
			entries = append(entries, sitemodel.SitemapEntry{
				Path:       "/t/" + t.ID,
				LastModMS:  t.UpdatedAt,
				ChangeFreq: "daily",
			})
			stats.Threads++
		}
	}

	data, err := sitemodel.BuildSitemap(baseURL, entries)
	return data, stats, err
}
