package projections

import (
	"fmt"
	"sort"
	"strings"
)

const (
	DerivedViewCommunityStats       = "community_stats"
	DerivedViewCommunityStatHistory = "community_stat_history"
	DerivedViewLatestFeed           = "feeds.latest"
	DerivedViewResidentFeed         = "feeds.resident"
	DerivedViewBoardSummaries       = "summaries.boards"
	DerivedViewUnreadThreads        = "summaries.unread_threads"
	DerivedViewBoardRankings        = "rankings.boards"
	DerivedViewThreadRankings       = "rankings.threads"
	DerivedViewReplyRankings        = "rankings.replies"
	DerivedViewUserRankings         = "rankings.users"
	DerivedViewBlessingRankings     = "rankings.blessings"
	DerivedViewArchiveRankings      = "rankings.archives"
	DerivedViewPostSearch           = "search.posts"
	DerivedViewDigestSearch         = "search.digest"
)

var knownDerivedViews = []string{
	DerivedViewCommunityStats,
	DerivedViewCommunityStatHistory,
	DerivedViewLatestFeed,
	DerivedViewResidentFeed,
	DerivedViewBoardSummaries,
	DerivedViewUnreadThreads,
	DerivedViewBoardRankings,
	DerivedViewThreadRankings,
	DerivedViewReplyRankings,
	DerivedViewUserRankings,
	DerivedViewBlessingRankings,
	DerivedViewArchiveRankings,
	DerivedViewPostSearch,
	DerivedViewDigestSearch,
}

var derivedViewGroups = map[string][]string{
	"community": {
		DerivedViewCommunityStats,
		DerivedViewCommunityStatHistory,
	},
	"feeds": {
		DerivedViewLatestFeed,
		DerivedViewResidentFeed,
	},
	"rankings": {
		DerivedViewBoardRankings,
		DerivedViewThreadRankings,
		DerivedViewReplyRankings,
		DerivedViewUserRankings,
		DerivedViewBlessingRankings,
		DerivedViewArchiveRankings,
	},
	"search": {
		DerivedViewPostSearch,
		DerivedViewDigestSearch,
	},
	"summaries": {
		DerivedViewBoardSummaries,
		DerivedViewUnreadThreads,
	},
}

func KnownDerivedViews() []string {
	return append([]string(nil), knownDerivedViews...)
}

func DerivedViewGroups() map[string][]string {
	out := make(map[string][]string, len(derivedViewGroups))
	for group, views := range derivedViewGroups {
		out[group] = append([]string(nil), views...)
	}
	return out
}

func ResolveDerivedViews(views []string) ([]string, error) {
	if len(views) == 0 {
		return nil, fmt.Errorf("derived view name required")
	}
	known := map[string]bool{}
	for _, view := range knownDerivedViews {
		known[view] = true
	}
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range views {
		for _, part := range strings.Split(raw, ",") {
			view := NormalizeDerivedView(part)
			if view == "" {
				continue
			}
			if view == "all" || view == "*" {
				return KnownDerivedViews(), nil
			}
			if group, ok := derivedViewGroups[view]; ok {
				for _, groupedView := range group {
					if !seen[groupedView] {
						out = append(out, groupedView)
						seen[groupedView] = true
					}
				}
				continue
			}
			if !known[view] {
				return nil, fmt.Errorf("unknown derived view %q", view)
			}
			if !seen[view] {
				out = append(out, view)
				seen[view] = true
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("derived view name required")
	}
	sort.Strings(out)
	return out, nil
}

func ContainsDerivedView(views []string, want string) bool {
	want = NormalizeDerivedView(want)
	for _, view := range views {
		if NormalizeDerivedView(view) == want {
			return true
		}
	}
	return false
}

func NormalizeDerivedView(view string) string {
	return strings.TrimSpace(strings.ToLower(view))
}
