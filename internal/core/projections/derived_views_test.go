package projections

import "testing"

func TestResolveDerivedViews(t *testing.T) {
	views, err := ResolveDerivedViews([]string{" rankings.threads ", DerivedViewBoardRankings, "rankings.threads"})
	if err != nil {
		t.Fatalf("ResolveDerivedViews: %v", err)
	}
	want := []string{DerivedViewBoardRankings, DerivedViewThreadRankings}
	assertDerivedViews(t, views, want)

	all, err := ResolveDerivedViews([]string{"all"})
	if err != nil {
		t.Fatalf("ResolveDerivedViews all: %v", err)
	}
	if len(all) != len(KnownDerivedViews()) {
		t.Fatalf("all view count = %d, want %d", len(all), len(KnownDerivedViews()))
	}

	search, err := ResolveDerivedViews([]string{"search"})
	if err != nil {
		t.Fatalf("ResolveDerivedViews search: %v", err)
	}
	assertDerivedViews(t, search, []string{DerivedViewDigestSearch, DerivedViewPostSearch})

	rankings, err := ResolveDerivedViews([]string{"rankings"})
	if err != nil {
		t.Fatalf("ResolveDerivedViews rankings: %v", err)
	}
	assertDerivedViews(t, rankings, []string{
		DerivedViewArchiveRankings,
		DerivedViewBlessingRankings,
		DerivedViewBoardRankings,
		DerivedViewReplyRankings,
		DerivedViewThreadRankings,
		DerivedViewUserRankings,
	})

	mixed, err := ResolveDerivedViews([]string{"search, summaries.boards", "summaries"})
	if err != nil {
		t.Fatalf("ResolveDerivedViews mixed groups: %v", err)
	}
	assertDerivedViews(t, mixed, []string{
		DerivedViewDigestSearch,
		DerivedViewPostSearch,
		DerivedViewBoardSummaries,
		DerivedViewUnreadThreads,
	})

	groups := DerivedViewGroups()
	if len(groups["search"]) != 2 {
		t.Fatalf("search group = %v, want two views", groups["search"])
	}
	if len(groups["feeds"]) != 2 {
		t.Fatalf("feeds group = %v, want two views", groups["feeds"])
	}
	groups["search"][0] = "mutated"
	if again := DerivedViewGroups()["search"][0]; again == "mutated" {
		t.Fatalf("DerivedViewGroups returned mutable backing slice")
	}

	if _, err := ResolveDerivedViews([]string{"rankings.unknown"}); err == nil {
		t.Fatal("expected unknown derived view to fail")
	}
}

func assertDerivedViews(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("views = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("views = %v, want %v", got, want)
		}
	}
}
