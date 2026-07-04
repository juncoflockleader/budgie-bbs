package proto

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func normalizeMsg[T any](normalize func(T) (T, string), payload T) string {
	_, msg := normalize(payload)
	return msg
}

func assertMsg(t *testing.T, name, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %q, want %q", name, got, want)
	}
}

func assertStringCases(t *testing.T, name string, normalize func(string) string, tests map[string]string) {
	t.Helper()
	for input, want := range tests {
		if got := normalize(input); got != want {
			t.Fatalf("%s(%q) = %q, want %q", name, input, got, want)
		}
	}
}

func assertValidStringCases(t *testing.T, name string, normalize func(string) (string, string), tests map[string]string) {
	t.Helper()
	for input, want := range tests {
		got, msg := normalize(input)
		if msg != "" || got != want {
			t.Fatalf("%s(%q) = %q, %q; want %q, valid", name, input, got, msg, want)
		}
	}
}

func assertStringResults(t *testing.T, name string, results map[string]string) {
	t.Helper()
	for want, got := range results {
		assertMsg(t, name, got, want)
	}
}

func assertValidValue[T comparable](t *testing.T, name string, got T, msg string, want T) {
	t.Helper()
	if msg != "" || got != want {
		t.Fatalf("%s = %#v, %q; want %#v, valid", name, got, msg, want)
	}
}

func assertNormalizedValue[T comparable](t *testing.T, name string, normalize func(T) (T, string), input, want T) {
	t.Helper()
	got, msg := normalize(input)
	assertValidValue(t, name, got, msg, want)
}

func assertValidDeepValue[T any](t *testing.T, name string, got T, msg string, want T) {
	t.Helper()
	if msg != "" || !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %#v, %q; want %#v, valid", name, got, msg, want)
	}
}

func assertNormalizedDeepValue[T any](t *testing.T, name string, normalize func(T) (T, string), input, want T) {
	t.Helper()
	got, msg := normalize(input)
	assertValidDeepValue(t, name, got, msg, want)
}

func assertSlice[T comparable](t *testing.T, name string, got, want []T) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s = %#v, want %#v", name, got, want)
	}
}

func assertStringPair(t *testing.T, name, gotFirst, gotSecond, wantFirst, wantSecond string) {
	t.Helper()
	if gotFirst != wantFirst || gotSecond != wantSecond {
		t.Fatalf("%s = %q/%q, want %q/%q", name, gotFirst, gotSecond, wantFirst, wantSecond)
	}
}

func assertTrimmedStringCap(t *testing.T, name string, normalize func(string) (string, string), limit int, lengthMessage string) {
	t.Helper()
	want := strings.Repeat("x", limit)
	got, msg := normalize(" " + want + " ")
	assertMsg(t, name+" msg", msg, "")
	if got != want {
		t.Fatalf("%s = %q, want trimmed limit string", name, got)
	}
	assertMsg(t, name+" over limit", normalizeMsg(normalize, strings.Repeat("x", limit+1)), lengthMessage)
}

func TestValidatePostBodyLength(t *testing.T) {
	assertMsg(t, "ValidatePostBodyLength at limit", ValidatePostBodyLength(strings.Repeat("x", MaxPostBodyLength)), "")
	assertMsg(t, "ValidatePostBodyLength over limit", ValidatePostBodyLength(strings.Repeat("x", MaxPostBodyLength+1)), postBodyLengthValidationMessage)
}

func TestValidateThreadTitle(t *testing.T) {
	assertMsg(t, "ValidateThreadTitle blank", ValidateThreadTitle(" \t "), threadTitleRequiredValidationMessage)
	assertMsg(t, "ValidateThreadTitle at limit", ValidateThreadTitle(strings.Repeat("x", MaxThreadTitleLength)), "")
	assertMsg(t, "ValidateThreadTitle over limit", ValidateThreadTitle(strings.Repeat("x", MaxThreadTitleLength+1)), threadTitleLengthValidationMessage)
}

func TestNormalizePostContentType(t *testing.T) {
	assertStringCases(t, "NormalizePostContentType", NormalizePostContentType, map[string]string{
		"ansi-art":   "ansi-art",
		"markup":     "markup",
		"":           "markup",
		" ansi-art ": "markup",
	})
}

func TestResolvePostAuthorIdentity(t *testing.T) {
	author, authorID, msg := ResolvePostAuthorIdentity("alice", "usr_alice", false, false, false)
	if msg != "" || author != "alice" || authorID != "usr_alice" {
		t.Fatalf("ResolvePostAuthorIdentity named = %q/%q msg %q, want actor identity", author, authorID, msg)
	}
	author, authorID, msg = ResolvePostAuthorIdentity("alice", "usr_alice", true, true, false)
	if msg != "" || author != "Anonymous" || authorID != "" {
		t.Fatalf("ResolvePostAuthorIdentity anonymous allowed = %q/%q msg %q, want anonymous identity", author, authorID, msg)
	}
	author, authorID, msg = ResolvePostAuthorIdentity("alice", "usr_alice", true, false, true)
	if msg != "" || author != "Anonymous" || authorID != "" {
		t.Fatalf("ResolvePostAuthorIdentity anonymous moderator = %q/%q msg %q, want anonymous identity", author, authorID, msg)
	}
	_, _, msg = ResolvePostAuthorIdentity("alice", "usr_alice", true, false, false)
	assertMsg(t, "ResolvePostAuthorIdentity disabled", msg, anonymousPostingDisabledMessage)
}

func TestNormalizePostSignature(t *testing.T) {
	assertMsg(t, "NormalizePostSignature trim", NormalizePostSignature("  goodbye  "), "goodbye")
	long := strings.Repeat("x", MaxPostSignatureLength+1)
	got := NormalizePostSignature(long)
	if len(got) != MaxPostSignatureLength || got != strings.Repeat("x", MaxPostSignatureLength) {
		t.Fatalf("NormalizePostSignature length = %d, want %d", len(got), MaxPostSignatureLength)
	}
}

func TestFormatQuotedReplyPrefix(t *testing.T) {
	got := FormatQuotedReplyPrefix(" alice ", " first line\r\n\nsecond line ")
	want := "> alice wrote:\n> first line\n>\n> second line\n\n"
	assertMsg(t, "FormatQuotedReplyPrefix", got, want)
	assertMsg(t, "FormatQuotedReplyPrefix empty", FormatQuotedReplyPrefix(" ", " "), "> Unknown wrote:\n> [empty article]\n\n")
	longBody := strings.TrimSuffix(strings.Repeat("line\n", MaxQuotedReplyLines+1), "\n")
	got = FormatQuotedReplyPrefix("alice", longBody)
	if !strings.Contains(got, "> ...\n") {
		t.Fatalf("FormatQuotedReplyPrefix long body = %q, want ellipsis", got)
	}
}

func TestFormatRepostBody(t *testing.T) {
	got := FormatRepostBody("general", "Thread title", "alice", "pst_1", " source body ")
	want := "Reposted from general / Thread title\nOriginal author: alice\nOriginal post: pst_1\n\n source body"
	assertMsg(t, "FormatRepostBody", got, want)
}

func TestFormatArticleMailBackBody(t *testing.T) {
	got := FormatArticleMailBackBody("general", "Thread title", "pst_1", "pst_2", "bob", " reply body ")
	want := "Article reply mail-back\n\nBoard: general\nThread: Thread title\nOriginal post: pst_1\nReply post: pst_2\nReply author: bob\n\n reply body"
	assertMsg(t, "FormatArticleMailBackBody", got, want)
}

func TestFormatSystemNoticeBody(t *testing.T) {
	got := FormatSystemNoticeBody(SystemNoticeBoard{Name: "Notepad"}, "Heads up", "body", "ops", "admin")
	want := "# Heads up\n\n- Notice board: Notepad\n- Actor: admin\n- Source: ops\n\nbody\n\nGenerated public system notice.\n"
	assertMsg(t, "FormatSystemNoticeBody", got, want)
	got = FormatSystemNoticeBody(SystemNoticeBoard{Name: "Notepad"}, "Heads up", "body\n", "", "admin")
	want = "# Heads up\n\n- Notice board: Notepad\n- Actor: admin\n\nbody\n\nGenerated public system notice.\n"
	assertMsg(t, "FormatSystemNoticeBody without source", got, want)
}

func TestFormatBlessingSystemBody(t *testing.T) {
	threadID, postID := BlessingSystemPostIDs("bless_1")
	assertStringPair(t, "BlessingSystemPostIDs", threadID, postID, "blessing_thr_bless_1", "blessing_pst_bless_1")
	assertMsg(t, "BlessingSystemTitle", BlessingSystemTitle("alice", "bob"), "Blessing: alice -> bob")
	got := FormatBlessingSystemBody("alice", "bob", " good luck ")
	want := "# Blessing for bob\n\n- From: alice\n- To: bob\n\ngood luck\n\nGenerated public blessing record.\n"
	assertMsg(t, "FormatBlessingSystemBody", got, want)
	got = FormatBlessingSystemBody("alice", "bob", " ")
	want = "# Blessing for bob\n\n- From: alice\n- To: bob\n\nA public blessing was sent.\n\nGenerated public blessing record.\n"
	assertMsg(t, "FormatBlessingSystemBody fallback", got, want)
}

func TestFormatPostAuthorMailBody(t *testing.T) {
	got := FormatPostAuthorMailBody("general", "Thread title", 42, "pst_42", "alice", "bob", " question ", " article body ")
	want := "question\n\n---\nSent from article reading context.\nBoard: general\nThread: Thread title\nPost: #42 (pst_42)\nArticle author: alice\nMail author: bob\n\nArticle excerpt:\narticle body"
	assertMsg(t, "FormatPostAuthorMailBody", got, want)
	long := strings.TrimSuffix(strings.Repeat("word ", MaxPostAuthorMailExcerptLength), " ")
	got = ArticleMailExcerpt(long, 4)
	assertMsg(t, "ArticleMailExcerpt", got, "word...")
	assertMsg(t, "ArticleMailExcerpt zero max", ArticleMailExcerpt(" body ", 0), "body")
}

func TestFormatSysmailSystemBody(t *testing.T) {
	got := FormatSysmailSystemBody("mail_1", "Campus bulletin", "body", "admin", 1)
	want := "# Sysop mail: Campus bulletin\n\n- Mail: mail_1\n- From: admin\n- Recipients: 1 user\n- Source: admin mail-all broadcast\n\nbody\n\nGenerated restricted sysop mail record.\n"
	assertMsg(t, "FormatSysmailSystemBody single", got, want)
	got = FormatSysmailSystemBody("mail_1", "Campus bulletin", "body\n", "admin", 2)
	want = "# Sysop mail: Campus bulletin\n\n- Mail: mail_1\n- From: admin\n- Recipients: 2 users\n- Source: admin mail-all broadcast\n\nbody\n\nGenerated restricted sysop mail record.\n"
	assertMsg(t, "FormatSysmailSystemBody plural", got, want)
}

func TestFormatSyssecuritySystemBody(t *testing.T) {
	got := FormatSyssecuritySystemBody(" Board settings changed ", []string{
		" Action: board settings changed ",
		"",
		"Actor: admin",
	})
	want := "# Board settings changed\n\n- Action: board settings changed\n- Actor: admin\n\nGenerated security notices omit private notes and article content.\n"
	assertMsg(t, "FormatSyssecuritySystemBody", got, want)

	got = FormatSyssecuritySystemBody(" ", nil)
	want = "# Security notice\n\n\nGenerated security notices omit private notes and article content.\n"
	assertMsg(t, "FormatSyssecuritySystemBody fallback", got, want)
}

func TestFormatSanctionSystemBody(t *testing.T) {
	got := FormatSanctionSystemBody("Deny post", "alice", "General", "general", "mute", "admin", " noisy ")
	want := "# Deny post\n\n- Action: deny post\n- User: alice\n- Board: General (general)\n- Kind: mute\n- Actor: admin\n- Reason: noisy\n\nGenerated public board-posting sanction record. Private moderation notes and article bodies are not mirrored.\n"
	assertMsg(t, "FormatSanctionSystemBody", got, want)
	got = FormatSanctionSystemBody("Undeny post", "alice", "General", "general", " ", "admin", " ")
	want = "# Undeny post\n\n- Action: undeny post\n- User: alice\n- Board: General (general)\n- Kind: all\n- Actor: admin\n\nGenerated public board-posting sanction record. Private moderation notes and article bodies are not mirrored.\n"
	assertMsg(t, "FormatSanctionSystemBody all", got, want)
}

func TestFormatPrivateMailBodies(t *testing.T) {
	got := FormatForwardMailBody(" please read ", "alice", []string{"bob", "carol"}, "Campus plans", []string{"plan.txt"}, " body ")
	want := "please read\n\n----- Forwarded mail -----\nFrom: alice\nTo: bob, carol\nSubject: Campus plans\nAttachments: plan.txt\n\nbody"
	assertMsg(t, "FormatForwardMailBody", got, want)
	got = FormatMailBoardBody(" ", "alice", nil, "Campus plans", nil, " body ")
	want = "Posted from private mail.\nFrom: alice\nSubject: Campus plans\n\nbody"
	assertMsg(t, "FormatMailBoardBody", got, want)
}

func TestFormatContentFilterReviewBody(t *testing.T) {
	threadID, postID := ContentFilterReviewPostIDs("rev_1")
	assertStringPair(t, "ContentFilterReviewPostIDs", threadID, postID, "filter_thr_rev_1", "filter_pst_rev_1")
	title := ContentFilterReviewTitle("rev_1")
	assertMsg(t, "ContentFilterReviewTitle", title, "Content filter review rev_1")
	got := FormatContentFilterReviewBody(title, "rev_1", "filter_1", "global", "general", "thr_1", "pst_1", "alice")
	want := "# Content filter review rev_1\n\n- Review: rev_1\n- Status: opened\n- Filter: filter_1\n- Filter scope: global\n- Board: general\n- Thread: thr_1\n- Post: pst_1\n- Public author: alice\n\nSensitive filter pattern and article body are kept out of this generated record.\n"
	assertMsg(t, "FormatContentFilterReviewBody", got, want)
}

func TestFormatModerationSystemBody(t *testing.T) {
	threadID, postID := ModerationSystemPostIDs(ModerationLogFlag, "rev_1")
	assertStringPair(t, "ModerationSystemPostIDs", threadID, postID, "mod_flag_thr_rev_1", "mod_flag_pst_rev_1")
	assertMsg(t, "ModerationSystemTitle resolve", ModerationSystemTitle(ModerationLogResolve, "rev_1"), "Moderation resolved rev_1")
	got := FormatModerationSystemBody(ModerationLogResolve, "rev_1", "general", "thr_1", "pst_1", "mod")
	want := "# Moderation resolved rev_1\n\n- Review: rev_1\n- Status: resolved\n- Board: general\n- Thread: thr_1\n- Post: pst_1\n- Actor: mod\n\nSensitive report and resolution text is kept in the moderator review queue.\n"
	assertMsg(t, "FormatModerationSystemBody", got, want)
}

func TestNormalizeSetThreadTitlePayload(t *testing.T) {
	assertNormalizedValue(t, "NormalizeSetThreadTitlePayload valid", NormalizeSetThreadTitlePayload,
		SetThreadTitlePayload{Thread: " thr_1 ", Title: " hello "},
		SetThreadTitlePayload{Thread: "thr_1", Title: "hello"})
	assertMsg(t, "blank thread", normalizeMsg(NormalizeSetThreadTitlePayload, SetThreadTitlePayload{Thread: " ", Title: "hello"}), threadRequiredValidationMessage)
	assertMsg(t, "blank title", normalizeMsg(NormalizeSetThreadTitlePayload, SetThreadTitlePayload{Thread: "thr_1", Title: " "}), threadTitleRequiredValidationMessage)
	assertMsg(t, "long title", normalizeMsg(NormalizeSetThreadTitlePayload, SetThreadTitlePayload{
		Thread: "thr_1",
		Title:  strings.Repeat("x", MaxThreadTitleLength+1),
	}), threadTitleLengthValidationMessage)
}

func TestNormalizeMoveThreadPayload(t *testing.T) {
	assertNormalizedValue(t, "NormalizeLockThreadPayload valid", NormalizeLockThreadPayload,
		LockThreadPayload{Thread: " thr_1 ", Locked: true},
		LockThreadPayload{Thread: "thr_1", Locked: true})
	assertMsg(t, "blank lock thread", normalizeMsg(NormalizeLockThreadPayload, LockThreadPayload{Thread: " "}), threadRequiredValidationMessage)

	assertNormalizedValue(t, "NormalizeMoveThreadPayload valid", NormalizeMoveThreadPayload,
		MoveThreadPayload{Thread: " thr_1 ", ToBoard: " archive "},
		MoveThreadPayload{Thread: "thr_1", ToBoard: "archive"})
	assertMsg(t, "blank thread", normalizeMsg(NormalizeMoveThreadPayload, MoveThreadPayload{Thread: " ", ToBoard: "archive"}), threadRequiredValidationMessage)
	assertMsg(t, "blank destination", normalizeMsg(NormalizeMoveThreadPayload, MoveThreadPayload{Thread: "thr_1", ToBoard: " "}), destinationBoardRequiredValidationMessage)
}

func TestNormalizeCreateThreadPayload(t *testing.T) {
	assertNormalizedDeepValue(t, "NormalizeCreateThreadPayload valid", NormalizeCreateThreadPayload,
		CreateThreadPayload{Board: " board1 ", Title: " hello ", Body: "  body  "},
		CreateThreadPayload{Board: "board1", Title: "hello", Body: "  body  "})
	assertMsg(t, "blank board", normalizeMsg(NormalizeCreateThreadPayload, CreateThreadPayload{Board: " ", Title: "title", Body: "body"}), createThreadRequiredValidationMessage)
	assertMsg(t, "blank title", normalizeMsg(NormalizeCreateThreadPayload, CreateThreadPayload{Board: "board1", Title: " ", Body: "body"}), createThreadRequiredValidationMessage)
	assertMsg(t, "blank body", normalizeMsg(NormalizeCreateThreadPayload, CreateThreadPayload{Board: "board1", Title: "title"}), createThreadRequiredValidationMessage)
	assertMsg(t, "long body", normalizeMsg(NormalizeCreateThreadPayload, CreateThreadPayload{
		Board: "board1",
		Title: "title",
		Body:  strings.Repeat("x", MaxPostBodyLength+1),
	}), postBodyLengthValidationMessage)
}

func TestNormalizeAppendPostPayload(t *testing.T) {
	assertNormalizedDeepValue(t, "NormalizeAppendPostPayload valid", NormalizeAppendPostPayload,
		AppendPostPayload{Thread: " thr_1 ", ReplyTo: " pst_1 ", Body: "  reply  "},
		AppendPostPayload{Thread: "thr_1", ReplyTo: "pst_1", Body: "  reply  "})
	assertMsg(t, "blank thread", normalizeMsg(NormalizeAppendPostPayload, AppendPostPayload{Thread: " ", Body: "reply"}), appendPostRequiredValidationMessage)
	assertMsg(t, "blank body", normalizeMsg(NormalizeAppendPostPayload, AppendPostPayload{Thread: "thr_1"}), appendPostRequiredValidationMessage)
	assertMsg(t, "long body", normalizeMsg(NormalizeAppendPostPayload, AppendPostPayload{
		Thread: "thr_1",
		Body:   strings.Repeat("x", MaxPostBodyLength+1),
	}), postBodyLengthValidationMessage)
}

func TestNormalizePostBoardMailPayload(t *testing.T) {
	assertNormalizedDeepValue(t, "NormalizePostBoardMailPayload valid", NormalizePostBoardMailPayload,
		PostBoardMailPayload{Board: " board1 ", Thread: " thr_1 ", Subject: " hello ", Body: " body ", ContentType: " markup "},
		PostBoardMailPayload{Board: "board1", Thread: "thr_1", Subject: "hello", Body: "body", ContentType: "markup"})
	assertMsg(t, "blank body", normalizeMsg(NormalizePostBoardMailPayload, PostBoardMailPayload{Board: "board1", Body: " "}), bodyRequiredValidationMessage)
}

func TestNormalizeRepostPostPayload(t *testing.T) {
	assertNormalizedValue(t, "NormalizeRepostPostPayload valid", NormalizeRepostPostPayload,
		RepostPostPayload{Post: " pst_1 ", Board: " board1 ", Title: " hello "},
		RepostPostPayload{Post: "pst_1", Board: "board1", Title: "hello"})
	assertMsg(t, "blank post", normalizeMsg(NormalizeRepostPostPayload, RepostPostPayload{Post: " ", Board: "board1"}), repostPostRequiredValidationMessage)
	assertMsg(t, "blank board", normalizeMsg(NormalizeRepostPostPayload, RepostPostPayload{Post: "pst_1", Board: " "}), repostPostRequiredValidationMessage)
}

func TestNormalizeEditPostPayload(t *testing.T) {
	assertNormalizedValue(t, "NormalizeEditPostPayload valid", NormalizeEditPostPayload,
		EditPostPayload{Post: " pst_1 ", Body: "  replacement  "},
		EditPostPayload{Post: "pst_1", Body: "  replacement  "})
	assertMsg(t, "blank post", normalizeMsg(NormalizeEditPostPayload, EditPostPayload{Post: " ", Body: "replacement"}), editPostRequiredValidationMessage)
	assertMsg(t, "blank body", normalizeMsg(NormalizeEditPostPayload, EditPostPayload{Post: "pst_1"}), editPostRequiredValidationMessage)
}

func TestNormalizeSetPostFlagPayload(t *testing.T) {
	noReply := false
	assertNormalizedValue(t, "NormalizeSetPostFlagPayload valid", NormalizeSetPostFlagPayload,
		SetPostFlagPayload{Post: " pst_1 ", NoReply: &noReply},
		SetPostFlagPayload{Post: "pst_1", NoReply: &noReply})
	assertMsg(t, "blank post", normalizeMsg(NormalizeSetPostFlagPayload, SetPostFlagPayload{Post: " ", NoReply: &noReply}), postRequiredValidationMessage)
	assertMsg(t, "no flags", normalizeMsg(NormalizeSetPostFlagPayload, SetPostFlagPayload{Post: "pst_1"}), postFlagRequiredValidationMessage)
}

func TestNormalizeModerationReviewPayloads(t *testing.T) {
	assertNormalizedValue(t, "NormalizeFlagPostPayload valid", NormalizeFlagPostPayload,
		FlagPostPayload{Post: " pst_1 ", Reason: "  reporter text  "},
		FlagPostPayload{Post: "pst_1", Reason: "  reporter text  "})
	assertMsg(t, "blank post", normalizeMsg(NormalizeFlagPostPayload, FlagPostPayload{Post: " "}), postRequiredValidationMessage)

	assertNormalizedValue(t, "NormalizeResolveReviewPayload valid", NormalizeResolveReviewPayload,
		ResolveReviewPayload{Review: " rev_1 ", Resolution: " resolved "},
		ResolveReviewPayload{Review: "rev_1", Resolution: "resolved"})
	assertMsg(t, "blank review", normalizeMsg(NormalizeResolveReviewPayload, ResolveReviewPayload{Review: " ", Resolution: "resolved"}), reviewAndResolutionRequiredValidationMessage)
	assertMsg(t, "blank resolution", normalizeMsg(NormalizeResolveReviewPayload, ResolveReviewPayload{Review: "rev_1", Resolution: " "}), reviewAndResolutionRequiredValidationMessage)
}

func TestNormalizePublishPollResultPayload(t *testing.T) {
	assertNormalizedValue(t, "NormalizePublishPollResultPayload valid", NormalizePublishPollResultPayload,
		PublishPollResultPayload{Poll: " poll_1 "},
		PublishPollResultPayload{Poll: "poll_1"})
	assertMsg(t, "blank poll", normalizeMsg(NormalizePublishPollResultPayload, PublishPollResultPayload{Poll: " "}), pollRequiredValidationMessage)
}

func TestFormatPollResultBody(t *testing.T) {
	threadID, postID := PollResultPostIDs("poll_1")
	assertStringPair(t, "PollResultPostIDs", threadID, postID, "vote_poll_poll_1", "vote_poll_post_poll_1")
	assertMsg(t, "PollResultTitle question", PollResultTitle(" Best option? "), "Poll result: Best option?")
	assertMsg(t, "PollResultTitle blank", PollResultTitle(" "), "Poll result")
	body := FormatPollResultBody("Vote result poll", "general", " Best option? ", []PollResultOption{
		{Text: "Option A", Votes: 1},
		{Text: "Option B", Votes: 2},
	})
	for _, want := range []string{
		"# Poll result: Best option?",
		"- Source thread: Vote result poll",
		"- Source board: general",
		"- Total votes: 3",
		"1. Option A: 1 vote(s), 33%",
		"2. Option B: 2 vote(s), 66%",
		"Generated public poll result.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("FormatPollResultBody missing %q:\n%s", want, body)
		}
	}

	body = FormatPollResultBody("", "", " ", nil)
	if !strings.Contains(body, "# Poll result: Untitled poll") || !strings.Contains(body, "- Total votes: 0") {
		t.Fatalf("FormatPollResultBody empty = %q, want untitled zero-vote body", body)
	}
}

func TestNormalizeRedactPostPayload(t *testing.T) {
	assertNormalizedValue(t, "NormalizeRedactPostPayload valid", NormalizeRedactPostPayload,
		RedactPostPayload{Post: " pst_1 ", Reason: "  reason  "},
		RedactPostPayload{Post: "pst_1", Reason: "  reason  "})
	assertMsg(t, "blank post", normalizeMsg(NormalizeRedactPostPayload, RedactPostPayload{Post: " "}), postRequiredValidationMessage)
}

func TestNormalizeRestorePostPayload(t *testing.T) {
	assertNormalizedValue(t, "NormalizeRestorePostPayload valid", NormalizeRestorePostPayload,
		RestorePostPayload{Post: " pst_1 "},
		RestorePostPayload{Post: "pst_1"})
	assertMsg(t, "blank post", normalizeMsg(NormalizeRestorePostPayload, RestorePostPayload{Post: " "}), postRequiredValidationMessage)
}

func TestNormalizePurgePostPayload(t *testing.T) {
	assertNormalizedValue(t, "NormalizePurgePostPayload valid", NormalizePurgePostPayload,
		PurgePostPayload{Post: " pst_1 ", Reason: "  reason  "},
		PurgePostPayload{Post: "pst_1", Reason: "  reason  "})
	assertMsg(t, "blank post", normalizeMsg(NormalizePurgePostPayload, PurgePostPayload{Post: " "}), postRequiredValidationMessage)
}

func TestNormalizeRedactPostRangePayload(t *testing.T) {
	payload, msg := NormalizeRedactPostRangePayload(RedactPostRangePayload{
		Board:  " board1 ",
		Posts:  []string{" pst_1 ", "pst_2", "pst_1"},
		Reason: "  reason  ",
	})
	assertValidDeepValue(t, "NormalizeRedactPostRangePayload valid", payload, msg,
		RedactPostRangePayload{Board: "board1", Posts: []string{"pst_1", "pst_2"}, Reason: "  reason  "})
	assertMsg(t, "blank board", normalizeMsg(NormalizeRedactPostRangePayload, RedactPostRangePayload{Board: " ", Posts: []string{"pst_1"}}), boardRequiredValidationMessage)
	assertMsg(t, "blank posts", normalizeMsg(NormalizeRedactPostRangePayload, RedactPostRangePayload{Board: "board1"}), postRangeRequiredValidationMessage)
}

func TestNormalizeRestorePostRangePayload(t *testing.T) {
	payload, msg := NormalizeRestorePostRangePayload(RestorePostRangePayload{
		Board: " board1 ",
		Posts: []string{" pst_1 ", "pst_2", "pst_1"},
	})
	assertValidDeepValue(t, "NormalizeRestorePostRangePayload valid", payload, msg,
		RestorePostRangePayload{Board: "board1", Posts: []string{"pst_1", "pst_2"}})
	assertMsg(t, "blank board", normalizeMsg(NormalizeRestorePostRangePayload, RestorePostRangePayload{Board: " ", Posts: []string{"pst_1"}}), boardRequiredValidationMessage)
	assertMsg(t, "blank post id", normalizeMsg(NormalizeRestorePostRangePayload, RestorePostRangePayload{Board: "board1", Posts: []string{"pst_1", " "}}), postRangeEmptyIDValidationMessage)
}

func TestNormalizeClearBoardJunkPayload(t *testing.T) {
	payload, msg := NormalizeClearBoardJunkPayload(ClearBoardJunkPayload{
		Board: " board1 ",
		Posts: []string{" pst_1 "},
	})
	assertValidDeepValue(t, "NormalizeClearBoardJunkPayload valid", payload, msg,
		ClearBoardJunkPayload{Board: "board1", Posts: []string{" pst_1 "}})
	assertMsg(t, "blank board", normalizeMsg(NormalizeClearBoardJunkPayload, ClearBoardJunkPayload{Board: " "}), boardRequiredValidationMessage)
}

func TestValidateSystemNoticeSource(t *testing.T) {
	assertMsg(t, "ValidateSystemNoticeSource at trimmed limit", ValidateSystemNoticeSource(" "+strings.Repeat("x", MaxSystemNoticeSourceLength)+" "), "")
	assertMsg(t, "over limit", ValidateSystemNoticeSource(strings.Repeat("x", MaxSystemNoticeSourceLength+1)), systemNoticeSourceLengthValidationMessage)
}

func TestNormalizeSystemNoticeBoard(t *testing.T) {
	tests := []struct {
		raw  string
		want SystemNoticeBoard
	}{
		{
			raw:  "",
			want: SystemNoticeBoard{ID: "notepad", Name: "notepad", Description: "Generated public system notes"},
		},
		{
			raw:  " NOTEPAD ",
			want: SystemNoticeBoard{ID: "notepad", Name: "notepad", Description: "Generated public system notes"},
		},
		{
			raw:  "giveup_notice",
			want: SystemNoticeBoard{ID: "GiveupNotice", Name: "GiveupNotice", Description: "Generated give-up-net notices"},
		},
		{
			raw:  "GiveupNotice",
			want: SystemNoticeBoard{ID: "GiveupNotice", Name: "GiveupNotice", Description: "Generated give-up-net notices"},
		},
		{
			raw:  "bbsnet",
			want: SystemNoticeBoard{ID: "bbsnet", Name: "bbsnet", Description: "Generated site-hop and network notices"},
		},
	}
	for _, tt := range tests {
		got, msg := NormalizeSystemNoticeBoard(tt.raw)
		assertValidValue(t, "NormalizeSystemNoticeBoard("+tt.raw+")", got, msg, tt.want)
	}
	_, msg := NormalizeSystemNoticeBoard("Filter")
	assertMsg(t, "invalid board", msg, systemNoticeBoardValidationMessage)
}

func TestNormalizePublishSystemNoticePayload(t *testing.T) {
	payload, board, msg := NormalizePublishSystemNoticePayload(PublishSystemNoticePayload{
		Board:  " giveup_notice ",
		Title:  " Maintenance ",
		Body:   " Please reconnect after 02:00 UTC. ",
		Source: " ops ",
	})
	if msg != "" || board.ID != "GiveupNotice" || payload.Board != "giveup_notice" ||
		payload.Title != "Maintenance" || payload.Body != "Please reconnect after 02:00 UTC." || payload.Source != "ops" {
		t.Fatalf("NormalizePublishSystemNoticePayload valid = %#v, %+v, %q; want normalized payload", payload, board, msg)
	}
	_, _, msg = NormalizePublishSystemNoticePayload(PublishSystemNoticePayload{Board: "Filter", Title: "Title", Body: "Body"})
	assertMsg(t, "invalid board", msg, systemNoticeBoardValidationMessage)
	_, _, msg = NormalizePublishSystemNoticePayload(PublishSystemNoticePayload{Title: " ", Body: "Body"})
	assertMsg(t, "blank title", msg, threadTitleRequiredValidationMessage)
	_, _, msg = NormalizePublishSystemNoticePayload(PublishSystemNoticePayload{Title: "Title", Body: " "})
	assertMsg(t, "blank body", msg, bodyRequiredValidationMessage)
	_, _, msg = NormalizePublishSystemNoticePayload(PublishSystemNoticePayload{Title: "Title", Body: strings.Repeat("x", MaxPostBodyLength+1)})
	assertMsg(t, "long body", msg, postBodyLengthValidationMessage)
	_, _, msg = NormalizePublishSystemNoticePayload(PublishSystemNoticePayload{Title: "Title", Body: "Body", Source: strings.Repeat("x", MaxSystemNoticeSourceLength+1)})
	assertMsg(t, "long source", msg, systemNoticeSourceLengthValidationMessage)
}

func TestValidateAttachmentCounts(t *testing.T) {
	assertMsg(t, "ValidatePostAttachmentCount at limit", ValidatePostAttachmentCount(MaxPostAttachments), "")
	assertMsg(t, "post attachment over limit", ValidatePostAttachmentCount(MaxPostAttachments+1), postAttachmentCountValidationMessage)
	assertMsg(t, "ValidateMailAttachmentCount at limit", ValidateMailAttachmentCount(MaxMailAttachments), "")
	assertMsg(t, "mail attachment over limit", ValidateMailAttachmentCount(MaxMailAttachments+1), mailAttachmentCountValidationMessage)
}

func TestValidSlug(t *testing.T) {
	valid := []string{"general", "board_1", "board-2", strings.Repeat("a", MaxSlugLength)}
	for _, slug := range valid {
		if !ValidSlug(slug) {
			t.Fatalf("ValidSlug(%q) = false, want true", slug)
		}
	}
	invalid := []string{"", "Upper", "has space", "has.dot", strings.Repeat("a", MaxSlugLength+1)}
	for _, slug := range invalid {
		if ValidSlug(slug) {
			t.Fatalf("ValidSlug(%q) = true, want false", slug)
		}
	}
}

func TestValidateSlugMessages(t *testing.T) {
	assertMsg(t, "ValidateSlugID valid", ValidateSlugID("board_1"), "")
	assertMsg(t, "invalid slug id", ValidateSlugID("Upper"), slugIDValidationMessage)
	assertMsg(t, "ValidateContentFilterID valid", ValidateContentFilterID("filter_1"), "")
	assertMsg(t, "invalid content filter id", ValidateContentFilterID("bad filter"), contentFilterIDValidationMessage)
}

func TestNormalizeCreateBoardPayload(t *testing.T) {
	assertNormalizedValue(t, "NormalizeCreateBoardPayload", NormalizeCreateBoardPayload, CreateBoardPayload{ID: " general ", Name: " General ", ParentID: " root "}, CreateBoardPayload{ID: "general", Name: "General", ParentID: "root"})

	assertMsg(t, "missing id", normalizeMsg(NormalizeCreateBoardPayload, CreateBoardPayload{Name: "General"}), createBoardRequiredValidationMessage)
	assertMsg(t, "missing name", normalizeMsg(NormalizeCreateBoardPayload, CreateBoardPayload{ID: "general"}), createBoardRequiredValidationMessage)
	assertMsg(t, "invalid id", normalizeMsg(NormalizeCreateBoardPayload, CreateBoardPayload{ID: "bad id", Name: "General"}), slugIDValidationMessage)
	assertMsg(t, "own parent", normalizeMsg(NormalizeCreateBoardPayload, CreateBoardPayload{ID: "general", Name: "General", ParentID: " general "}), boardOwnParentValidationMessage)
	negative := -1
	assertMsg(t, "negative position", normalizeMsg(NormalizeCreateBoardPayload, CreateBoardPayload{ID: "general", Name: "General", Position: &negative}), positionNegativeValidationMessage)
}

func TestNormalizeContentFilterPayload(t *testing.T) {
	got := NormalizeContentFilterPayload(SetContentFilterPayload{
		ID:      " filter_1 ",
		Pattern: " secret ",
		Scope:   " general ",
	})
	if got.ID != "filter_1" || got.Pattern != "secret" || got.Scope != "general" {
		t.Fatalf("NormalizeContentFilterPayload scoped = %+v", got)
	}
	got = NormalizeContentFilterPayload(SetContentFilterPayload{Pattern: " secret "})
	if got.Pattern != "secret" || got.Scope != DefaultContentFilterScope {
		t.Fatalf("NormalizeContentFilterPayload global = %+v", got)
	}
}

func TestValidateContentFilterPattern(t *testing.T) {
	assertMsg(t, "blank pattern", ValidateContentFilterPattern(" \t "), contentFilterPatternRequiredValidationMessage)
	assertMsg(t, "ValidateContentFilterPattern at limit", ValidateContentFilterPattern(strings.Repeat("x", MaxContentFilterPatternLength)), "")
	assertMsg(t, "over limit", ValidateContentFilterPattern(strings.Repeat("x", MaxContentFilterPatternLength+1)), contentFilterPatternLengthValidationMessage)
}

func TestNormalizeUserRelationshipKind(t *testing.T) {
	tests := map[string]string{
		" friend ":    "friend",
		"friends":     "friend",
		"following":   "friend",
		" ignore ":    "ignore",
		"badlist":     "ignore",
		"unsupported": "",
	}
	assertStringCases(t, "NormalizeUserRelationshipKind", NormalizeUserRelationshipKind, tests)
}

func TestNormalizeUserRelationshipNote(t *testing.T) {
	assertTrimmedStringCap(t, "NormalizeUserRelationshipNote", NormalizeUserRelationshipNote, MaxUserRelationshipNoteLength, userRelationshipNoteLengthValidationMessage)
}

func TestNormalizeSetUserRelationshipPayload(t *testing.T) {
	assertNormalizedValue(t, "NormalizeSetUserRelationshipPayload valid", NormalizeSetUserRelationshipPayload, SetUserRelationshipPayload{User: " alice ", Kind: " friends ", Note: " hello "}, SetUserRelationshipPayload{User: "alice", Kind: "friend", Note: "hello"})
	assertMsg(t, "blank user", normalizeMsg(NormalizeSetUserRelationshipPayload, SetUserRelationshipPayload{User: " ", Kind: "friend"}), userRequiredValidationMessage)
	assertMsg(t, "invalid kind", normalizeMsg(NormalizeSetUserRelationshipPayload, SetUserRelationshipPayload{User: "alice", Kind: "stranger"}), userRelationshipKindValidationMessage)
	assertMsg(t, "long note", normalizeMsg(NormalizeSetUserRelationshipPayload, SetUserRelationshipPayload{
		User: "alice",
		Kind: "friend",
		Note: strings.Repeat("x", MaxUserRelationshipNoteLength+1),
	}), userRelationshipNoteLengthValidationMessage)
}

func TestNormalizeSetLoginWatchPayload(t *testing.T) {
	assertNormalizedValue(t, "NormalizeSetLoginWatchPayload valid", NormalizeSetLoginWatchPayload, SetLoginWatchPayload{User: " alice ", Active: true}, SetLoginWatchPayload{User: "alice", Active: true})
	assertMsg(t, "blank user", normalizeMsg(NormalizeSetLoginWatchPayload, SetLoginWatchPayload{User: " "}), userRequiredValidationMessage)
}

func TestPresenceStatusPredicates(t *testing.T) {
	if !HiddenPresenceStatus(" invisible ") || !HiddenPresenceStatus("OFFLINE") {
		t.Fatal("HiddenPresenceStatus did not recognize hidden statuses")
	}
	if !CloakedPresenceStatus(" cloaked ") || !CloakedPresenceStatus("CLOAK") {
		t.Fatal("CloakedPresenceStatus did not recognize cloak statuses")
	}
	if !TypingPresenceStatus(" Typing ") {
		t.Fatal("TypingPresenceStatus did not recognize typing")
	}
	if VisiblePresenceStatus(" ") || VisiblePresenceStatus("offline") || VisiblePresenceStatus("cloaked") {
		t.Fatal("VisiblePresenceStatus returned true for blank/hidden/cloaked status")
	}
	if !VisiblePresenceStatus("reading:general") {
		t.Fatal("VisiblePresenceStatus returned false for visible status")
	}
}

func TestNormalizeBlessingMessage(t *testing.T) {
	assertTrimmedStringCap(t, "NormalizeBlessingMessage", NormalizeBlessingMessage, MaxBlessingMessageLength, blessingMessageLengthValidationMessage)
}

func TestNormalizeBlessUserPayload(t *testing.T) {
	assertNormalizedValue(t, "NormalizeBlessUserPayload valid", NormalizeBlessUserPayload, BlessUserPayload{User: " alice ", Message: " cheers "}, BlessUserPayload{User: "alice", Message: "cheers"})
	assertMsg(t, "blank user", normalizeMsg(NormalizeBlessUserPayload, BlessUserPayload{User: " ", Message: "cheers"}), userRequiredValidationMessage)
	assertMsg(t, "long message", normalizeMsg(NormalizeBlessUserPayload, BlessUserPayload{
		User:    "alice",
		Message: strings.Repeat("x", MaxBlessingMessageLength+1),
	}), blessingMessageLengthValidationMessage)
}

func TestNormalizeSetBoardFavoritePayload(t *testing.T) {
	position := 2
	assertNormalizedValue(t, "NormalizeSetBoardFavoritePayload valid", NormalizeSetBoardFavoritePayload, SetBoardFavoritePayload{Board: " board1 ", Favorite: true, FolderID: " folder1 ", Position: &position}, SetBoardFavoritePayload{Board: "board1", Favorite: true, FolderID: "folder1", Position: &position})
	assertMsg(t, "blank board", normalizeMsg(NormalizeSetBoardFavoritePayload, SetBoardFavoritePayload{Board: " "}), boardRequiredValidationMessage)
}

func TestNormalizeSetBoardZapPayload(t *testing.T) {
	assertNormalizedValue(t, "NormalizeSetBoardZapPayload valid", NormalizeSetBoardZapPayload, SetBoardZapPayload{Board: " board1 ", Zapped: true}, SetBoardZapPayload{Board: "board1", Zapped: true})
	assertMsg(t, "blank board", normalizeMsg(NormalizeSetBoardZapPayload, SetBoardZapPayload{Board: " "}), boardRequiredValidationMessage)
}

func TestNormalizeSetBoardSettingsPayload(t *testing.T) {
	readOnly := true
	assertNormalizedValue(t, "NormalizeSetBoardSettingsPayload valid", NormalizeSetBoardSettingsPayload, SetBoardSettingsPayload{Board: " board1 ", ReadOnly: &readOnly}, SetBoardSettingsPayload{Board: "board1", ReadOnly: &readOnly})
	assertMsg(t, "blank board", normalizeMsg(NormalizeSetBoardSettingsPayload, SetBoardSettingsPayload{Board: " "}), boardRequiredValidationMessage)
}

func TestBoardSettingsAuditLines(t *testing.T) {
	readOnly := true
	zapAllowed := false
	guestAccess := " PUBLIC "
	lines := BoardSettingsAuditLines(SetBoardSettingsPayload{
		ReadOnly:    &readOnly,
		ZapAllowed:  &zapAllowed,
		GuestAccess: &guestAccess,
	})
	assertSlice(t, "BoardSettingsAuditLines", lines, []string{"readOnly: true", "zapAllowed: false", "guestAccess: public"})

	guestAccess = "default"
	lines = BoardSettingsAuditLines(SetBoardSettingsPayload{GuestAccess: &guestAccess})
	if len(lines) != 1 || lines[0] != "guestAccess: default" {
		t.Fatalf("BoardSettingsAuditLines default guest access = %#v", lines)
	}
}

func TestNormalizeCreateFavoriteFolderPayload(t *testing.T) {
	position := 3
	assertNormalizedValue(t, "NormalizeCreateFavoriteFolderPayload valid", NormalizeCreateFavoriteFolderPayload, CreateFavoriteFolderPayload{Name: " Work ", ParentID: " root ", Position: &position}, CreateFavoriteFolderPayload{Name: "Work", ParentID: "root", Position: &position})
	assertMsg(t, "blank name", normalizeMsg(NormalizeCreateFavoriteFolderPayload, CreateFavoriteFolderPayload{Name: " "}), favoriteFolderNameRequiredValidationMessage)
}

func TestNormalizeUpdateFavoriteFolderPayload(t *testing.T) {
	parentID := " parent "
	position := 4
	payload, msg := NormalizeUpdateFavoriteFolderPayload(UpdateFavoriteFolderPayload{
		Folder:   " folder1 ",
		Name:     " Projects ",
		ParentID: &parentID,
		Position: &position,
	})
	if msg != "" || payload.Folder != "folder1" || payload.Name != "Projects" || payload.ParentID == nil || *payload.ParentID != "parent" || payload.Position != &position {
		t.Fatalf("NormalizeUpdateFavoriteFolderPayload valid = %#v, %q; want trimmed payload", payload, msg)
	}
	assertMsg(t, "blank folder", normalizeMsg(NormalizeUpdateFavoriteFolderPayload, UpdateFavoriteFolderPayload{Folder: " "}), folderRequiredValidationMessage)
	assertMsg(t, "long name", normalizeMsg(NormalizeUpdateFavoriteFolderPayload, UpdateFavoriteFolderPayload{
		Folder: "folder1",
		Name:   strings.Repeat("x", MaxFavoriteFolderNameLength+1),
	}), favoriteFolderNameLengthValidationMessage)
}

func TestNormalizeDeleteFavoriteFolderPayload(t *testing.T) {
	assertNormalizedValue(t, "NormalizeDeleteFavoriteFolderPayload valid", NormalizeDeleteFavoriteFolderPayload, DeleteFavoriteFolderPayload{Folder: " folder1 "}, DeleteFavoriteFolderPayload{Folder: "folder1"})
	assertMsg(t, "blank folder", normalizeMsg(NormalizeDeleteFavoriteFolderPayload, DeleteFavoriteFolderPayload{Folder: " "}), folderRequiredValidationMessage)
}

func TestNormalizeMoveBoardFavoritePayload(t *testing.T) {
	position := 5
	assertNormalizedValue(t, "NormalizeMoveBoardFavoritePayload valid", NormalizeMoveBoardFavoritePayload, MoveBoardFavoritePayload{Board: " board1 ", FolderID: " folder1 ", Position: &position}, MoveBoardFavoritePayload{Board: "board1", FolderID: "folder1", Position: &position})
	assertMsg(t, "blank board", normalizeMsg(NormalizeMoveBoardFavoritePayload, MoveBoardFavoritePayload{Board: " "}), boardRequiredValidationMessage)
}

func TestNormalizeImportFavoriteTreePayload(t *testing.T) {
	replace := true
	payload, msg := NormalizeImportFavoriteTreePayload(ImportFavoriteTreePayload{
		Replace: &replace,
		Folders: []ImportFavoriteFolderPayload{
			{ID: " root ", Name: " Root ", Position: 1},
			{ID: " child ", ParentID: " root ", Name: " Child "},
		},
		Boards: []ImportFavoriteBoardPayload{
			{ID: " tech ", FolderID: " child ", Position: 3},
			{ID: " life "},
		},
	})
	assertMsg(t, "NormalizeImportFavoriteTreePayload valid msg", msg, "")
	if payload.Folders[0].ID != "root" || payload.Folders[0].Name != "Root" ||
		payload.Folders[1].ID != "child" || payload.Folders[1].ParentID != "root" || payload.Folders[1].Name != "Child" ||
		payload.Boards[0].ID != "tech" || payload.Boards[0].FolderID != "child" || payload.Boards[1].ID != "life" ||
		payload.Replace != &replace {
		t.Fatalf("NormalizeImportFavoriteTreePayload = %#v, want trimmed payload", payload)
	}

	assertMsg(t, "missing folder id", normalizeMsg(NormalizeImportFavoriteTreePayload, ImportFavoriteTreePayload{
		Folders: []ImportFavoriteFolderPayload{{Name: "Missing"}},
	}), "folder 1 is missing id")
	assertMsg(t, "duplicate folder", normalizeMsg(NormalizeImportFavoriteTreePayload, ImportFavoriteTreePayload{
		Folders: []ImportFavoriteFolderPayload{{ID: "src", Name: "One"}, {ID: " src ", Name: "Two"}},
	}), `duplicate folder id "src"`)
	assertMsg(t, "missing parent", normalizeMsg(NormalizeImportFavoriteTreePayload, ImportFavoriteTreePayload{
		Folders: []ImportFavoriteFolderPayload{{ID: "child", ParentID: "missing", Name: "Child"}},
	}), `folder "child" references missing parent "missing"`)
	assertMsg(t, "cycle", normalizeMsg(NormalizeImportFavoriteTreePayload, ImportFavoriteTreePayload{
		Folders: []ImportFavoriteFolderPayload{{ID: "a", ParentID: "b", Name: "A"}, {ID: "b", ParentID: "a", Name: "B"}},
	}), favoriteFolderImportCycleValidationMessage)
	assertMsg(t, "missing board folder", normalizeMsg(NormalizeImportFavoriteTreePayload, ImportFavoriteTreePayload{
		Boards: []ImportFavoriteBoardPayload{{ID: "tech", FolderID: "missing"}},
	}), `board "tech" references missing folder "missing"`)
	assertMsg(t, "missing board id", normalizeMsg(NormalizeImportFavoriteTreePayload, ImportFavoriteTreePayload{
		Boards: []ImportFavoriteBoardPayload{{ID: " "}},
	}), favoriteBoardIDRequiredValidationMessage)
}

func TestNormalizeDirectMessagePolicy(t *testing.T) {
	tests := map[string]string{
		"":             "all",
		" everyone ":   "all",
		"friends-only": "friends",
		"friend_only":  "friends",
		" disabled ":   "none",
		"block":        "none",
	}
	assertValidStringCases(t, "NormalizeDirectMessagePolicy", NormalizeDirectMessagePolicy, tests)
	assertMsg(t, "invalid", normalizeMsg(NormalizeDirectMessagePolicy, "strangers"), directMessagePolicyValidationMessage)
}

func TestNormalizeSendDirectMessagePayload(t *testing.T) {
	assertNormalizedValue(t, "NormalizeSendDirectMessagePayload valid", NormalizeSendDirectMessagePayload, SendDirectMessagePayload{To: " alice ", Body: " hello "}, SendDirectMessagePayload{To: "alice", Body: "hello"})
	assertMsg(t, "blank to", normalizeMsg(NormalizeSendDirectMessagePayload, SendDirectMessagePayload{To: " ", Body: "hello"}), directMessageRecipientAndBodyValidationMessage)
	assertMsg(t, "blank body", normalizeMsg(NormalizeSendDirectMessagePayload, SendDirectMessagePayload{To: "alice", Body: " "}), directMessageRecipientAndBodyValidationMessage)
}

func TestNormalizeDirectMessageTarget(t *testing.T) {
	assertNormalizedValue(t, "NormalizeDirectMessageTarget valid", NormalizeDirectMessageTarget, " dm_1 ", "dm_1")
	assertMsg(t, "blank target", normalizeMsg(NormalizeDirectMessageTarget, " "), messageRequiredValidationMessage)
}

func TestDirectConversationID(t *testing.T) {
	assertMsg(t, "DirectConversationID bob/alice", DirectConversationID("bob", "alice"), "alice:bob")
	assertMsg(t, "DirectConversationID alice/bob", DirectConversationID("alice", "bob"), "alice:bob")
}

func TestDirectMessageEventScopes(t *testing.T) {
	assertSlice(t, "DirectMessageEventScopes two users", DirectMessageEventScopes("alice", "bob"), []string{"account:alice", "account:bob"})
	assertSlice(t, "DirectMessageEventScopes self", DirectMessageEventScopes("alice", "alice"), []string{"account:alice"})
}

func TestNormalizeMailbox(t *testing.T) {
	tests := map[string]string{
		" Inbox ":  "inbox",
		"delete":   "trash",
		" deleted": "trash",
		"keep_1":   "keep_1",
	}
	assertValidStringCases(t, "NormalizeMailbox", NormalizeMailbox, tests)
	assertMsg(t, "blank", normalizeMsg(NormalizeMailbox, " \t "), mailboxRequiredValidationMessage)
	assertMsg(t, "long", normalizeMsg(NormalizeMailbox, strings.Repeat("x", MaxMailboxLength+1)), mailboxLengthValidationMessage)
	assertMsg(t, "invalid chars", normalizeMsg(NormalizeMailbox, "in.box"), mailboxCharactersValidationMessage)
}

func TestNormalizeUpdateMailPayload(t *testing.T) {
	read := true
	mailbox := " deleted "
	got, msg := NormalizeUpdateMailPayload(UpdateMailPayload{
		Mail:    " mail_1 ",
		Mailbox: &mailbox,
		Read:    &read,
	})
	assertMsg(t, "NormalizeUpdateMailPayload valid msg", msg, "")
	if got.Mail != "mail_1" || got.Mailbox == nil || *got.Mailbox != "trash" || got.Read == nil || !*got.Read {
		t.Fatalf("NormalizeUpdateMailPayload valid = %+v, want trimmed mail and trash mailbox", got)
	}
	assertMsg(t, "blank mail", normalizeMsg(NormalizeUpdateMailPayload, UpdateMailPayload{Mail: " "}), mailRequiredValidationMessage)
	assertMsg(t, "no mutation", normalizeMsg(NormalizeUpdateMailPayload, UpdateMailPayload{Mail: "mail_1"}), updateMailOperationRequiredValidationMessage)
	badMailbox := "in.box"
	assertMsg(t, "bad mailbox", normalizeMsg(NormalizeUpdateMailPayload, UpdateMailPayload{
		Mail:    "mail_1",
		Mailbox: &badMailbox,
	}), mailboxCharactersValidationMessage)
}

func TestNormalizeDeleteMailPayload(t *testing.T) {
	assertNormalizedValue(t, "NormalizeDeleteMailPayload valid", NormalizeDeleteMailPayload, DeleteMailPayload{Mail: " mail_1 "}, DeleteMailPayload{Mail: "mail_1"})
	assertMsg(t, "blank mail", normalizeMsg(NormalizeDeleteMailPayload, DeleteMailPayload{Mail: " "}), mailRequiredValidationMessage)
}

func TestNormalizeForwardMailPayload(t *testing.T) {
	got, msg := NormalizeForwardMailPayload(ForwardMailPayload{
		Mail:    " mail_1 ",
		Subject: " Subject ",
		Note:    " Note ",
	})
	assertValidDeepValue(t, "NormalizeForwardMailPayload valid", got, msg, ForwardMailPayload{Mail: "mail_1", Subject: "Subject", Note: "Note"})
	assertMsg(t, "blank mail", normalizeMsg(NormalizeForwardMailPayload, ForwardMailPayload{Mail: " "}), mailRequiredValidationMessage)
}

func TestNormalizeForwardMailSubject(t *testing.T) {
	tests := map[string]string{
		"Reply":             NormalizeForwardMailSubject(" Reply ", "Source"),
		"Fwd: Source":       NormalizeForwardMailSubject("", " Source "),
		"fwd: Source":       NormalizeForwardMailSubject("", " fwd: Source "),
		"Fwd: (no subject)": NormalizeForwardMailSubject("", " \t "),
	}
	assertStringResults(t, "NormalizeForwardMailSubject", tests)
}

func TestNormalizePostMailToBoardPayload(t *testing.T) {
	got, mailMsg, targetMsg := NormalizePostMailToBoardPayload(PostMailToBoardPayload{
		Mail:    " mail_1 ",
		Board:   " general ",
		Thread:  " thread_1 ",
		Subject: " Subject ",
		Note:    " Note ",
	})
	if mailMsg != "" || targetMsg != "" || got.Mail != "mail_1" || got.Board != "general" || got.Thread != "thread_1" ||
		got.Subject != "Subject" || got.Note != "Note" {
		t.Fatalf("NormalizePostMailToBoardPayload = %+v, %q, %q; want trimmed fields", got, mailMsg, targetMsg)
	}
	_, mailMsg, targetMsg = NormalizePostMailToBoardPayload(PostMailToBoardPayload{Mail: " ", Board: "general"})
	if mailMsg != mailRequiredValidationMessage || targetMsg != "" {
		t.Fatalf("NormalizePostMailToBoardPayload blank mail = %q, %q; want mail required only", mailMsg, targetMsg)
	}
	_, mailMsg, targetMsg = NormalizePostMailToBoardPayload(PostMailToBoardPayload{Mail: "mail_1", Board: " ", Thread: " "})
	if mailMsg != "" || targetMsg != boardRequiredValidationMessage {
		t.Fatalf("NormalizePostMailToBoardPayload blank target = %q, %q; want board required only", mailMsg, targetMsg)
	}
}

func TestPostMailToBoardTitle(t *testing.T) {
	tests := map[string]string{
		"Explicit":     PostMailToBoardTitle(" Explicit ", "Source"),
		"Source":       PostMailToBoardTitle("", " Source "),
		"(no subject)": PostMailToBoardTitle(" \t ", " \t "),
	}
	assertStringResults(t, "PostMailToBoardTitle", tests)
}

func TestNormalizeMailPostAuthorPayload(t *testing.T) {
	assertNormalizedValue(t, "NormalizeMailPostAuthorPayload valid", NormalizeMailPostAuthorPayload, MailPostAuthorPayload{Post: " post_1 ", Subject: " Question ", Body: " Please reply. "}, MailPostAuthorPayload{Post: "post_1", Subject: "Question", Body: "Please reply."})
	assertMsg(t, "blank post", normalizeMsg(NormalizeMailPostAuthorPayload, MailPostAuthorPayload{Post: " ", Body: "body"}), postRequiredValidationMessage)
	assertMsg(t, "blank body", normalizeMsg(NormalizeMailPostAuthorPayload, MailPostAuthorPayload{Post: "post_1", Body: " "}), bodyRequiredValidationMessage)
}

func TestMailPostAuthorSubject(t *testing.T) {
	tests := map[string]string{
		"Question":   MailPostAuthorSubject(" Question ", "Thread title"),
		"Re: Thread": MailPostAuthorSubject("", "Thread"),
		"Re: ":       MailPostAuthorSubject(" \t ", ""),
	}
	assertStringResults(t, "MailPostAuthorSubject", tests)
}

func TestNormalizeSendDigestEntryMailPayload(t *testing.T) {
	got, msg := NormalizeSendDigestEntryMailPayload(SendDigestEntryMailPayload{
		Entry:   " digest_1 ",
		Subject: " Subject ",
		Note:    " Note ",
	})
	assertValidDeepValue(t, "NormalizeSendDigestEntryMailPayload valid", got, msg, SendDigestEntryMailPayload{Entry: "digest_1", Subject: "Subject", Note: "Note"})
	assertMsg(t, "blank entry", normalizeMsg(NormalizeSendDigestEntryMailPayload, SendDigestEntryMailPayload{Entry: " "}), entryRequiredValidationMessage)
}

func TestDigestEntryMailSubject(t *testing.T) {
	tests := map[string]string{
		"Archive note":   DigestEntryMailSubject(" Archive note ", "Digest title"),
		"Archive: Entry": DigestEntryMailSubject("", "Entry"),
		"Archive: ":      DigestEntryMailSubject(" \t ", ""),
	}
	assertStringResults(t, "DigestEntryMailSubject", tests)
}

func TestNormalizeSendMailContentPayload(t *testing.T) {
	got, msg := NormalizeSendMailContentPayload(SendMailPayload{
		Subject: " Subject ",
		Body:    " Body ",
		ReplyTo: " mail_1 ",
	})
	assertValidDeepValue(t, "NormalizeSendMailContentPayload valid", got, msg, SendMailPayload{Subject: "Subject", Body: "Body", ReplyTo: "mail_1"})
	got, msg = NormalizeSendMailContentPayload(SendMailPayload{Body: " Body "})
	assertValidDeepValue(t, "NormalizeSendMailContentPayload default subject", got, msg, SendMailPayload{Subject: "(no subject)", Body: "Body"})
	assertMsg(t, "blank body", normalizeMsg(NormalizeSendMailContentPayload, SendMailPayload{Body: " \t "}), bodyRequiredValidationMessage)
}

func TestNormalizeMailGroupPayload(t *testing.T) {
	got, msg := NormalizeMailGroupPayload(SetMailGroupPayload{
		Group:   " lab ",
		Name:    " Lab Team ",
		Members: []string{"alice", "bob"},
	})
	assertMsg(t, "NormalizeMailGroupPayload valid msg", msg, "")
	if got.Group != "lab" || got.Name != "Lab Team" || len(got.Members) != 2 {
		t.Fatalf("NormalizeMailGroupPayload valid = %+v, want trimmed group/name", got)
	}
	assertMsg(t, "blank name", normalizeMsg(NormalizeMailGroupPayload, SetMailGroupPayload{Name: " \t "}), mailGroupNameRequiredValidationMessage)
	assertMsg(t, "long name", normalizeMsg(NormalizeMailGroupPayload, SetMailGroupPayload{Name: strings.Repeat("x", MaxMailGroupNameLength+1)}), mailGroupNameLengthValidationMessage)
	assertMsg(t, "many members", normalizeMsg(NormalizeMailGroupPayload, SetMailGroupPayload{
		Name:    "lab",
		Members: make([]string, MaxMailGroupMembers+1),
	}), mailGroupMemberCountValidationMessage)
}

func TestNormalizeDeleteMailGroupPayload(t *testing.T) {
	assertNormalizedValue(t, "NormalizeDeleteMailGroupPayload valid", NormalizeDeleteMailGroupPayload, DeleteMailGroupPayload{Group: " lab "}, DeleteMailGroupPayload{Group: "lab"})
	assertMsg(t, "blank group", normalizeMsg(NormalizeDeleteMailGroupPayload, DeleteMailGroupPayload{Group: " "}), mailGroupRequiredValidationMessage)
}

func TestIsFriendMailGroupRef(t *testing.T) {
	for _, ref := range []string{"friend", " friends ", "@friends", "friend-list", "friends-list"} {
		if !IsFriendMailGroupRef(ref) {
			t.Fatalf("IsFriendMailGroupRef(%q) = false, want true", ref)
		}
	}
	if IsFriendMailGroupRef("lab") {
		t.Fatal("IsFriendMailGroupRef lab = true, want false")
	}
}

func TestNormalizePostRangeIDs(t *testing.T) {
	got, msg := NormalizePostRangeIDs([]string{" pst_1 ", "pst_2", "pst_1"})
	assertMsg(t, "NormalizePostRangeIDs valid msg", msg, "")
	assertSlice(t, "NormalizePostRangeIDs valid", got, []string{"pst_1", "pst_2"})
	assertMsg(t, "empty", normalizeMsg(NormalizePostRangeIDs, []string(nil)), postRangeRequiredValidationMessage)
	assertMsg(t, "long", normalizeMsg(NormalizePostRangeIDs, make([]string, MaxCommandRangeItems+1)), postRangeLengthValidationMessage)
	assertMsg(t, "blank id", normalizeMsg(NormalizePostRangeIDs, []string{"pst_1", " "}), postRangeEmptyIDValidationMessage)
}

func TestNormalizeMailRangeIDs(t *testing.T) {
	got, msg := NormalizeMailRangeIDs([]string{" mail_1 ", "mail_2", "mail_1"})
	assertMsg(t, "NormalizeMailRangeIDs valid msg", msg, "")
	assertSlice(t, "NormalizeMailRangeIDs valid", got, []string{"mail_1", "mail_2"})
	assertMsg(t, "empty", normalizeMsg(NormalizeMailRangeIDs, []string(nil)), mailRangeRequiredValidationMessage)
	assertMsg(t, "long", normalizeMsg(NormalizeMailRangeIDs, make([]string, MaxCommandRangeItems+1)), mailRangeLengthValidationMessage)
	assertMsg(t, "blank id", normalizeMsg(NormalizeMailRangeIDs, []string{"mail_1", " "}), mailRangeEmptyIDValidationMessage)
}

func TestNormalizeBoardMemberTitle(t *testing.T) {
	assertTrimmedStringCap(t, "NormalizeBoardMemberTitle", NormalizeBoardMemberTitle, MaxBoardMemberTitleLength, boardMemberTitleLengthValidationMessage)
}

func TestNormalizeSetBoardModeratorPayload(t *testing.T) {
	position := 3
	assertNormalizedValue(t, "NormalizeSetBoardModeratorPayload valid", NormalizeSetBoardModeratorPayload, SetBoardModeratorPayload{Board: " club ", User: " alice ", Moderator: true, Position: &position}, SetBoardModeratorPayload{Board: "club", User: "alice", Moderator: true, Position: &position})
	assertMsg(t, "blank user", normalizeMsg(NormalizeSetBoardModeratorPayload, SetBoardModeratorPayload{Board: "club", User: " "}), boardAndUserRequiredValidationMessage)
}

func TestNormalizeSetBoardMemberPayload(t *testing.T) {
	position := 4
	assertNormalizedValue(t, "NormalizeSetBoardMemberPayload valid", NormalizeSetBoardMemberPayload, SetBoardMemberPayload{Board: " club ", User: " bob ", Member: true, Title: " Regular ", Position: &position}, SetBoardMemberPayload{Board: "club", User: "bob", Member: true, Title: "Regular", Position: &position})
	assertMsg(t, "blank board", normalizeMsg(NormalizeSetBoardMemberPayload, SetBoardMemberPayload{Board: " ", User: "bob"}), boardAndUserRequiredValidationMessage)
	assertMsg(t, "long title", normalizeMsg(NormalizeSetBoardMemberPayload, SetBoardMemberPayload{
		Board: "club",
		User:  "bob",
		Title: strings.Repeat("x", MaxBoardMemberTitleLength+1),
	}), boardMemberTitleLengthValidationMessage)
	negativePosition := -1
	assertMsg(t, "negative position", normalizeMsg(NormalizeSetBoardMemberPayload, SetBoardMemberPayload{
		Board:    "club",
		User:     "bob",
		Position: &negativePosition,
	}), boardMemberPositionNegativeValidationMessage)
}

func TestSetBoardMemberPermissionsChanged(t *testing.T) {
	if SetBoardMemberPermissionsChanged(SetBoardMemberPayload{Title: "member"}) {
		t.Fatal("SetBoardMemberPermissionsChanged title-only payload = true, want false")
	}
	canCurate := false
	if !SetBoardMemberPermissionsChanged(SetBoardMemberPayload{CanCurate: &canCurate}) {
		t.Fatal("SetBoardMemberPermissionsChanged delegated permission payload = false, want true")
	}
	canSetSettings := true
	if !SetBoardMemberPermissionsChanged(SetBoardMemberPayload{CanSetBoardSettings: &canSetSettings}) {
		t.Fatal("SetBoardMemberPermissionsChanged board-settings permission payload = false, want true")
	}
}

func TestBoardMemberPermissionPolicyChecks(t *testing.T) {
	if failure := CheckBoardMemberManagerPermission(false, false); failure == nil || failure.Code != ErrForbidden || failure.Message != BoardMemberManagerPermissionMessage {
		t.Fatalf("CheckBoardMemberManagerPermission = %#v; want manager failure", failure)
	}
	if failure := CheckBoardMemberManagerPermission(false, true); failure != nil {
		t.Fatalf("CheckBoardMemberManagerPermission delegated manager = %#v; want nil", failure)
	}
	canCurate := true
	if failure := CheckSetBoardMemberPermissionChange(SetBoardMemberPayload{CanCurate: &canCurate}, false); failure == nil || failure.Message != BoardModeratorChangeMemberPermissions {
		t.Fatalf("CheckSetBoardMemberPermissionChange = %#v; want permission-change failure", failure)
	}
	if failure := CheckSetBoardMemberTargetPermission(false, true, false); failure == nil || failure.Message != BoardModeratorManageBoardModerators {
		t.Fatalf("CheckSetBoardMemberTargetPermission moderator = %#v; want moderator failure", failure)
	}
	if failure := CheckSetBoardMemberTargetPermission(false, false, true); failure == nil || failure.Message != BoardModeratorManageDelegatedMembers {
		t.Fatalf("CheckSetBoardMemberTargetPermission delegated = %#v; want delegated failure", failure)
	}
	if failure := CheckReviewBoardMembershipPermission(false, true, "user1", "user1", "approved"); failure == nil || failure.Message != BoardModeratorReviewOwnApplication {
		t.Fatalf("CheckReviewBoardMembershipPermission own = %#v; want own-review failure", failure)
	}
	if failure := CheckReviewBoardMembershipPermission(false, true, "mod1", "user1", "blacklisted"); failure == nil || failure.Message != BoardModeratorBlacklistMembership {
		t.Fatalf("CheckReviewBoardMembershipPermission blacklist = %#v; want blacklist failure", failure)
	}
	if failure := CheckReviewBoardMembershipPermission(true, false, "mod1", "mod1", "blacklisted"); failure != nil {
		t.Fatalf("CheckReviewBoardMembershipPermission moderator = %#v; want nil", failure)
	}
}

func TestNormalizeBoardMemberApprovalMode(t *testing.T) {
	tests := map[string]string{
		"":            "manual",
		" manual ":    "manual",
		"auto":        "auto",
		" automatic ": "auto",
	}
	assertValidStringCases(t, "NormalizeBoardMemberApprovalMode", NormalizeBoardMemberApprovalMode, tests)
	assertMsg(t, "invalid", normalizeMsg(NormalizeBoardMemberApprovalMode, "maybe"), boardMemberApprovalModeValidationMessage)
}

func TestNormalizeSetBoardMemberRequirementsPayload(t *testing.T) {
	approvalMode := " automatic "
	minScore := 7
	payload, msg := NormalizeSetBoardMemberRequirementsPayload(SetBoardMemberRequirementsPayload{
		Board:        " tech ",
		MinScore:     &minScore,
		ApprovalMode: &approvalMode,
	})
	if msg != "" || payload.Board != "tech" || payload.MinScore != &minScore || payload.ApprovalMode == nil || *payload.ApprovalMode != "auto" {
		t.Fatalf("NormalizeSetBoardMemberRequirementsPayload valid = %#v, %q; want trimmed payload", payload, msg)
	}
	assertMsg(t, "blank board", normalizeMsg(NormalizeSetBoardMemberRequirementsPayload, SetBoardMemberRequirementsPayload{Board: " "}), boardRequiredValidationMessage)
	negativeScore := -1
	assertMsg(t, "negative score", normalizeMsg(NormalizeSetBoardMemberRequirementsPayload, SetBoardMemberRequirementsPayload{
		Board:    "tech",
		MinScore: &negativeScore,
	}), "minScore must be non-negative")
	badApprovalMode := "maybe"
	assertMsg(t, "bad approval mode", normalizeMsg(NormalizeSetBoardMemberRequirementsPayload, SetBoardMemberRequirementsPayload{
		Board:        "tech",
		ApprovalMode: &badApprovalMode,
	}), boardMemberApprovalModeValidationMessage)
}

func TestNormalizeBoardMemberApplicationStatus(t *testing.T) {
	tests := map[string]string{
		" approve ":   "approved",
		"approved":    "approved",
		"reject":      "rejected",
		"rejected":    "rejected",
		"blacklist":   "blacklisted",
		"blacklisted": "blacklisted",
	}
	assertValidStringCases(t, "NormalizeBoardMemberApplicationStatus", NormalizeBoardMemberApplicationStatus, tests)
	assertMsg(t, "invalid", normalizeMsg(NormalizeBoardMemberApplicationStatus, "maybe"), boardMemberApplicationStatusValidationMessage)
}

func TestNormalizeReviewBoardMembershipTargetPayload(t *testing.T) {
	assertNormalizedValue(t, "NormalizeReviewBoardMembershipTargetPayload valid", NormalizeReviewBoardMembershipTargetPayload, ReviewBoardMembershipPayload{Application: " app1 ", Status: " approve "}, ReviewBoardMembershipPayload{Application: "app1", Status: "approved"})
	assertMsg(t, "blank application", normalizeMsg(NormalizeReviewBoardMembershipTargetPayload, ReviewBoardMembershipPayload{
		Application: " ",
		Status:      "approved",
	}), boardMembershipReviewRequiredValidationMessage)
	assertMsg(t, "bad status", normalizeMsg(NormalizeReviewBoardMembershipTargetPayload, ReviewBoardMembershipPayload{
		Application: "app1",
		Status:      "maybe",
	}), boardMemberApplicationStatusValidationMessage)
}

func TestNormalizeReviewBoardMembershipContent(t *testing.T) {
	assertNormalizedValue(t, "NormalizeReviewBoardMembershipContent valid", NormalizeReviewBoardMembershipContent, ReviewBoardMembershipPayload{Application: "app1", Status: "approved", Title: " Club regular ", Note: " Welcome aboard. "}, ReviewBoardMembershipPayload{Application: "app1", Status: "approved", Title: "Club regular", Note: "Welcome aboard."})
	assertMsg(t, "long title", normalizeMsg(NormalizeReviewBoardMembershipContent, ReviewBoardMembershipPayload{
		Title: strings.Repeat("x", MaxBoardMemberTitleLength+1),
	}), boardMemberTitleLengthValidationMessage)
	assertMsg(t, "long note", normalizeMsg(NormalizeReviewBoardMembershipContent, ReviewBoardMembershipPayload{
		Note: strings.Repeat("x", MaxBoardNoteLength+1),
	}), boardMembershipReviewNoteLengthValidationMessage)
}

func TestNormalizeFavoriteFolderName(t *testing.T) {
	normalizeRequired := func(name string) (string, string) {
		return NormalizeFavoriteFolderName(name, true)
	}
	assertTrimmedStringCap(t, "NormalizeFavoriteFolderName", normalizeRequired, MaxFavoriteFolderNameLength, favoriteFolderNameLengthValidationMessage)
	assertMsg(t, "blank required", normalizeMsg(normalizeRequired, " \t "), favoriteFolderNameRequiredValidationMessage)
	if got, msg := NormalizeFavoriteFolderName(" \t ", false); msg != "" || got != "" {
		t.Fatalf("NormalizeFavoriteFolderName blank optional = %q, %q; want blank valid", got, msg)
	}
}

func TestNormalizeStatsSnapshotDate(t *testing.T) {
	dateLabel, dateID, msg := NormalizeStatsSnapshotDate(" 2026-06-04 ", 0)
	if msg != "" || dateLabel != "2026-06-04" || dateID != "20260604" {
		t.Fatalf("NormalizeStatsSnapshotDate explicit = %q, %q, %q; want 2026-06-04, 20260604, valid", dateLabel, dateID, msg)
	}
	dateLabel, dateID, msg = NormalizeStatsSnapshotDate("", 1780617600000)
	if msg != "" || dateLabel != "2026-06-05" || dateID != "20260605" {
		t.Fatalf("NormalizeStatsSnapshotDate default = %q, %q, %q; want 2026-06-05, 20260605, valid", dateLabel, dateID, msg)
	}
	_, _, msg = NormalizeStatsSnapshotDate("06/04/2026", 0)
	assertMsg(t, "invalid", msg, statsSnapshotDateValidationMessage)
}

func TestNormalizeDigestKind(t *testing.T) {
	tests := map[string]string{
		"":               "digest",
		" archive ":      "archive",
		"recommended":    "recommended",
		"pinned":         "pinned",
		" announcement ": "announcement",
	}
	assertValidStringCases(t, "NormalizeDigestKind", NormalizeDigestKind, tests)
	assertMsg(t, "invalid", normalizeMsg(NormalizeDigestKind, "unknown"), digestKindValidationMessage)
}

func TestNormalizeDigestPath(t *testing.T) {
	assertMsg(t, "NormalizeDigestPath trimmed", NormalizeDigestPath(" /faq/howto/ "), "faq/howto")
	longPath := strings.Repeat("x", MaxDigestPathLength+10)
	if got := NormalizeDigestPath(longPath); len(got) != MaxDigestPathLength {
		t.Fatalf("NormalizeDigestPath long len = %d, want %d", len(got), MaxDigestPathLength)
	}
}

func TestNormalizeDigestPathMutationHelpers(t *testing.T) {
	board, msg := NormalizeDigestPathMutationBoard(" general ")
	if msg != "" || board != "general" {
		t.Fatalf("NormalizeDigestPathMutationBoard valid = %q, %q; want general, valid", board, msg)
	}
	assertMsg(t, "blank board", normalizeMsg(NormalizeDigestPathMutationBoard, " "), boardRequiredValidationMessage)

	kind, msg := NormalizeDigestPathMutationKind(" ")
	if msg != "" || kind != "archive" {
		t.Fatalf("NormalizeDigestPathMutationKind blank = %q, %q; want archive, valid", kind, msg)
	}
	kind, msg = NormalizeDigestPathMutationKind(" Announcement ")
	if msg != "" || kind != "announcement" {
		t.Fatalf("NormalizeDigestPathMutationKind announcement = %q, %q; want announcement, valid", kind, msg)
	}
	assertMsg(t, "invalid kind", normalizeMsg(NormalizeDigestPathMutationKind, "unknown"), digestKindValidationMessage)

	fromPath, toPath, msg := NormalizeDigestPathMutationPaths(" /archive/source/ ", " /archive/dest/ ")
	if msg != "" || fromPath != "archive/source" || toPath != "archive/dest" {
		t.Fatalf("NormalizeDigestPathMutationPaths valid = %q, %q, %q; want normalized paths", fromPath, toPath, msg)
	}
	assertMsg(t, "NormalizeDigestPathMutationSourcePath valid msg", normalizeMsg(NormalizeDigestPathMutationSourcePath, " /archive/source/ "), "")
	assertMsg(t, "blank source path", normalizeMsg(NormalizeDigestPathMutationSourcePath, " / "), sourcePathRequiredValidationMessage)
}

func TestNormalizeDigestCurationFields(t *testing.T) {
	kind, title, path, note, msg := NormalizeDigestCurationFields(" announcement ", " Big news ", " /news/today/ ", " Featured ")
	if msg != "" || kind != "announcement" || title != "Big news" || path != "news/today" || note != "Featured" {
		t.Fatalf("NormalizeDigestCurationFields valid = %q, %q, %q, %q, %q; want normalized fields", kind, title, path, note, msg)
	}
	_, _, _, _, msg = NormalizeDigestCurationFields("unknown", "", "", "")
	assertMsg(t, "invalid kind", msg, digestKindValidationMessage)
}

func TestNormalizeDigestCurationTargetPayloads(t *testing.T) {
	assertNormalizedValue(t, "NormalizeCuratePostTargetPayload valid", NormalizeCuratePostTargetPayload, CuratePostPayload{Post: " pst1 "}, CuratePostPayload{Post: "pst1"})
	assertMsg(t, "blank post", normalizeMsg(NormalizeCuratePostTargetPayload, CuratePostPayload{Post: " "}), postRequiredValidationMessage)
	assertNormalizedValue(t, "NormalizeCurateThreadTargetPayload valid", NormalizeCurateThreadTargetPayload, CurateThreadPayload{Thread: " thr1 "}, CurateThreadPayload{Thread: "thr1"})
	assertMsg(t, "blank thread", normalizeMsg(NormalizeCurateThreadTargetPayload, CurateThreadPayload{Thread: " "}), threadRequiredValidationMessage)
}

func TestNormalizeDigestEntryMaintenancePayloads(t *testing.T) {
	assertNormalizedValue(t, "NormalizeRemoveDigestEntryPayload valid", NormalizeRemoveDigestEntryPayload, RemoveDigestEntryPayload{Entry: " dig1 "}, RemoveDigestEntryPayload{Entry: "dig1"})
	assertMsg(t, "blank entry", normalizeMsg(NormalizeRemoveDigestEntryPayload, RemoveDigestEntryPayload{Entry: " "}), entryRequiredValidationMessage)

	title := " Updated "
	path := " /archive/updated/ "
	note := " Note "
	update, msg := NormalizeUpdateDigestEntryPayload(UpdateDigestEntryPayload{
		Entry: " dig1 ",
		Title: &title,
		Path:  &path,
		Note:  &note,
	})
	if msg != "" || update.Entry != "dig1" || update.Title == nil || *update.Title != "Updated" || update.Path == nil || *update.Path != "archive/updated" || update.Note == nil || *update.Note != "Note" {
		t.Fatalf("NormalizeUpdateDigestEntryPayload valid = %#v, %q; want normalized payload", update, msg)
	}
	assertMsg(t, "blank entry", normalizeMsg(NormalizeUpdateDigestEntryTargetPayload, UpdateDigestEntryPayload{Entry: " "}), entryRequiredValidationMessage)
	blankTitle := " "
	assertMsg(t, "blank title", normalizeMsg(NormalizeUpdateDigestEntryPayload, UpdateDigestEntryPayload{
		Entry: "dig1",
		Title: &blankTitle,
	}), threadTitleRequiredValidationMessage)

	assertNormalizedValue(t, "NormalizeSetDigestEntryBodyPayload valid", NormalizeSetDigestEntryBodyPayload, SetDigestEntryBodyPayload{Entry: " dig1 ", Body: " raw body "}, SetDigestEntryBodyPayload{Entry: "dig1", Body: " raw body "})
	assertMsg(t, "blank entry", normalizeMsg(NormalizeSetDigestEntryBodyPayload, SetDigestEntryBodyPayload{Entry: " "}), entryRequiredValidationMessage)
}

func TestDigestCurationPermissionMessage(t *testing.T) {
	assertMsg(t, "DigestCurationPermissionMessage announcement", DigestCurationPermissionMessage("announcement"), "board announcement permission required")
	assertMsg(t, "DigestCurationPermissionMessage digest", DigestCurationPermissionMessage("digest"), "board curator permission required")
}

func TestDigestEventScopes(t *testing.T) {
	assertSlice(t, "DigestEventScopes", DigestEventScopes("general"), []string{"board:general", "digest:general", "digest:global"})
}

func TestNormalizeModerationReason(t *testing.T) {
	assertTrimmedStringCap(t, "NormalizeModerationReason", NormalizeModerationReason, MaxModerationReasonLength, moderationReasonLengthValidationMessage)
}

func TestNormalizeSanctionPayloads(t *testing.T) {
	sanction, msg := NormalizeSanctionUserPayload(SanctionUserPayload{
		User:   " alice ",
		Kind:   " mute ",
		Scope:  " general ",
		Reason: " noisy ",
	})
	assertValidValue(t, "NormalizeSanctionUserPayload valid", sanction, msg, SanctionUserPayload{User: "alice", Kind: "mute", Scope: "general", Reason: "noisy"})
	sanction, msg = NormalizeSanctionUserPayload(SanctionUserPayload{User: "alice", Kind: "ban"})
	assertValidValue(t, "NormalizeSanctionUserPayload default scope", sanction, msg, SanctionUserPayload{User: "alice", Kind: "ban", Scope: "global"})
	assertMsg(t, "blank user", normalizeMsg(NormalizeSanctionUserPayload, SanctionUserPayload{User: " ", Kind: "mute"}), userRequiredValidationMessage)
	assertMsg(t, "bad kind", normalizeMsg(NormalizeSanctionUserPayload, SanctionUserPayload{User: "alice", Kind: "warn"}), sanctionKindValidationMessage)

	clear, msg := NormalizeClearUserSanctionPayload(ClearUserSanctionPayload{
		User:   " bob ",
		Kind:   " ban ",
		Scope:  " general ",
		Reason: " forgiven ",
	})
	assertValidValue(t, "NormalizeClearUserSanctionPayload valid", clear, msg, ClearUserSanctionPayload{User: "bob", Kind: "ban", Scope: "general", Reason: "forgiven"})
	clear, msg = NormalizeClearUserSanctionPayload(ClearUserSanctionPayload{User: "bob"})
	assertValidValue(t, "NormalizeClearUserSanctionPayload default scope", clear, msg, ClearUserSanctionPayload{User: "bob", Scope: "global"})
	assertMsg(t, "blank user", normalizeMsg(NormalizeClearUserSanctionPayload, ClearUserSanctionPayload{User: " "}), userRequiredValidationMessage)
	assertMsg(t, "bad kind", normalizeMsg(NormalizeClearUserSanctionPayload, ClearUserSanctionPayload{User: "bob", Kind: "warn"}), clearSanctionKindValidationMessage)
}

func TestNormalizeBoardNotes(t *testing.T) {
	tests := []struct {
		name      string
		normalize func(string) (string, string)
		want      string
	}{
		{name: "recommendation", normalize: NormalizeBoardRecommendationNote, want: boardRecommendationNoteLengthValidationMessage},
		{name: "application", normalize: NormalizeBoardMembershipApplicationNote, want: boardMembershipApplicationNoteLengthValidationMessage},
		{name: "review", normalize: NormalizeBoardMembershipReviewNote, want: boardMembershipReviewNoteLengthValidationMessage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertTrimmedStringCap(t, "board note", tt.normalize, MaxBoardNoteLength, tt.want)
		})
	}
}

func TestNormalizeSetRecommendedBoardPayload(t *testing.T) {
	position := 2
	assertNormalizedValue(t, "NormalizeSetRecommendedBoardPayload valid", NormalizeSetRecommendedBoardPayload, SetRecommendedBoardPayload{Board: " tech ", Recommended: true, Note: " Start here. ", Position: &position}, SetRecommendedBoardPayload{Board: "tech", Recommended: true, Note: "Start here.", Position: &position})
	assertMsg(t, "blank board", normalizeMsg(NormalizeSetRecommendedBoardPayload, SetRecommendedBoardPayload{Board: " "}), boardRequiredValidationMessage)
	negativePosition := -1
	assertMsg(t, "negative position", normalizeMsg(NormalizeSetRecommendedBoardPayload, SetRecommendedBoardPayload{
		Board:    "tech",
		Position: &negativePosition,
	}), positionNegativeValidationMessage)
	assertMsg(t, "long note", normalizeMsg(NormalizeSetRecommendedBoardPayload, SetRecommendedBoardPayload{
		Board: "tech",
		Note:  strings.Repeat("x", MaxBoardNoteLength+1),
	}), boardRecommendationNoteLengthValidationMessage)
}

func TestNormalizeApplyBoardMembershipPayload(t *testing.T) {
	assertNormalizedValue(t, "NormalizeApplyBoardMembershipPayload valid", NormalizeApplyBoardMembershipPayload, ApplyBoardMembershipPayload{Board: " general ", Note: " I read this board daily. "}, ApplyBoardMembershipPayload{Board: "general", Note: "I read this board daily."})
	assertMsg(t, "blank board", normalizeMsg(NormalizeApplyBoardMembershipPayload, ApplyBoardMembershipPayload{Board: " "}), boardRequiredValidationMessage)
	assertMsg(t, "long note", normalizeMsg(NormalizeApplyBoardMembershipPayload, ApplyBoardMembershipPayload{
		Board: "general",
		Note:  strings.Repeat("x", MaxBoardNoteLength+1),
	}), boardMembershipApplicationNoteLengthValidationMessage)
}

func TestNormalizeLeaveBoardMembershipPayload(t *testing.T) {
	assertNormalizedValue(t, "NormalizeLeaveBoardMembershipPayload valid", NormalizeLeaveBoardMembershipPayload, LeaveBoardMembershipPayload{Board: " general "}, LeaveBoardMembershipPayload{Board: "general"})
	assertMsg(t, "blank board", normalizeMsg(NormalizeLeaveBoardMembershipPayload, LeaveBoardMembershipPayload{Board: " "}), boardRequiredValidationMessage)
}

func TestNormalizeBoardReadPayloads(t *testing.T) {
	assertNormalizedValue(t, "NormalizeMarkBoardReadPayload valid", NormalizeMarkBoardReadPayload, MarkBoardReadPayload{Board: " general "}, MarkBoardReadPayload{Board: "general"})
	assertMsg(t, "blank mark board", normalizeMsg(NormalizeMarkBoardReadPayload, MarkBoardReadPayload{Board: " "}), boardRequiredValidationMessage)
	assertNormalizedValue(t, "NormalizeRestoreBoardReadPayload valid", NormalizeRestoreBoardReadPayload, RestoreBoardReadPayload{Board: " general "}, RestoreBoardReadPayload{Board: "general"})
	assertMsg(t, "blank restore board", normalizeMsg(NormalizeRestoreBoardReadPayload, RestoreBoardReadPayload{Board: " "}), boardRequiredValidationMessage)
}

func TestNormalizeThreadAndPostReadPayloads(t *testing.T) {
	assertNormalizedValue(t, "NormalizeMarkThreadReadPayload valid", NormalizeMarkThreadReadPayload, MarkThreadReadPayload{Thread: " thr1 "}, MarkThreadReadPayload{Thread: "thr1"})
	assertMsg(t, "blank mark thread", normalizeMsg(NormalizeMarkThreadReadPayload, MarkThreadReadPayload{Thread: " "}), threadRequiredValidationMessage)
	assertNormalizedValue(t, "NormalizeRestoreThreadReadPayload valid", NormalizeRestoreThreadReadPayload, RestoreThreadReadPayload{Thread: " thr1 "}, RestoreThreadReadPayload{Thread: "thr1"})
	assertMsg(t, "blank restore thread", normalizeMsg(NormalizeRestoreThreadReadPayload, RestoreThreadReadPayload{Thread: " "}), threadRequiredValidationMessage)
	assertNormalizedValue(t, "NormalizeMarkPostReadPayload valid", NormalizeMarkPostReadPayload, MarkPostReadPayload{Post: " pst1 "}, MarkPostReadPayload{Post: "pst1"})
	assertMsg(t, "blank mark post", normalizeMsg(NormalizeMarkPostReadPayload, MarkPostReadPayload{Post: " "}), postRequiredValidationMessage)
}

func TestNormalizeAttachmentPayload(t *testing.T) {
	got, msg := NormalizeAttachmentPayload(AttachmentPayload{
		Filename:    " proof.txt ",
		ContentType: " text/plain ",
		URL:         " https://cdn.example/proof.txt ",
		SizeBytes:   42,
	})
	want := AttachmentPayload{
		Filename:    "proof.txt",
		ContentType: "text/plain",
		URL:         "https://cdn.example/proof.txt",
		SizeBytes:   42,
	}
	assertValidValue(t, "NormalizeAttachmentPayload valid", got, msg, want)

	tests := []struct {
		name string
		item AttachmentPayload
		want string
	}{
		{name: "filename required", item: AttachmentPayload{Filename: " "}, want: attachmentFilenameRequiredValidationMessage},
		{name: "filename length", item: AttachmentPayload{Filename: strings.Repeat("x", MaxAttachmentFilenameLength+1)}, want: attachmentFilenameLengthValidationMessage},
		{name: "content type length", item: AttachmentPayload{Filename: "proof.txt", ContentType: strings.Repeat("x", MaxAttachmentContentTypeLength+1)}, want: attachmentContentTypeLengthValidationMessage},
		{name: "url length", item: AttachmentPayload{Filename: "proof.txt", URL: strings.Repeat("x", MaxAttachmentURLLength+1)}, want: attachmentURLLengthValidationMessage},
		{name: "size negative", item: AttachmentPayload{Filename: "proof.txt", SizeBytes: -1}, want: attachmentSizeValidationMessage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertMsg(t, "NormalizeAttachmentPayload msg", normalizeMsg(NormalizeAttachmentPayload, tt.item), tt.want)
		})
	}
}

func TestNormalizeAttachmentLists(t *testing.T) {
	postAttachments, msg := NormalizePostAttachments([]AttachmentPayload{{
		ID:          "client-id",
		Filename:    " proof.txt ",
		ContentType: " text/plain ",
		URL:         " https://cdn.example/proof.txt ",
		SizeBytes:   42,
	}})
	assertMsg(t, "NormalizePostAttachments valid msg", msg, "")
	wantPost := AttachmentPayload{
		Filename:    "proof.txt",
		ContentType: "text/plain",
		URL:         "https://cdn.example/proof.txt",
		SizeBytes:   42,
	}
	assertSlice(t, "NormalizePostAttachments valid", postAttachments, []AttachmentPayload{wantPost})
	withIDs := WithAttachmentIDs(postAttachments, func(i int) string {
		return "att_generated"
	})
	wantPost.ID = "att_generated"
	assertSlice(t, "WithAttachmentIDs", withIDs, []AttachmentPayload{wantPost})
	assertMsg(t, "WithAttachmentIDs source ID", postAttachments[0].ID, "")

	mailAttachments, msg := NormalizeMailAttachments([]AttachmentPayload{{
		ID:          "client-mail-id",
		Filename:    " plan.txt ",
		ContentType: " text/plain ",
		URL:         " https://cdn.example/plan.txt ",
		SizeBytes:   7,
	}})
	assertMsg(t, "NormalizeMailAttachments valid msg", msg, "")
	wantMail := AttachmentPayload{
		Filename:    "plan.txt",
		ContentType: "text/plain",
		URL:         "https://cdn.example/plan.txt",
		SizeBytes:   7,
	}
	assertSlice(t, "NormalizeMailAttachments valid", mailAttachments, []AttachmentPayload{wantMail})

	assertMsg(t, "post attachments over limit", normalizeMsg(NormalizePostAttachments, make([]AttachmentPayload, MaxPostAttachments+1)), postAttachmentCountValidationMessage)
	assertMsg(t, "mail attachments over limit", normalizeMsg(NormalizeMailAttachments, make([]AttachmentPayload, MaxMailAttachments+1)), mailAttachmentCountValidationMessage)
	assertMsg(t, "invalid post attachment", normalizeMsg(NormalizePostAttachments, []AttachmentPayload{{Filename: "proof.txt", SizeBytes: -1}}), attachmentSizeValidationMessage)
}

func TestMailMessageSize(t *testing.T) {
	got := MailMessageSize("subject", "body", []AttachmentPayload{
		{Filename: "a.txt", SizeBytes: 5},
		{Filename: "b.txt", SizeBytes: 7},
	})
	if got != int64(len("subject")+len("body")+12) {
		t.Fatalf("MailMessageSize = %d, want subject+body+attachments", got)
	}
	if got := MailMessageSize("", "", []AttachmentPayload{{SizeBytes: -1}}); got != 0 {
		t.Fatalf("MailMessageSize underflow clamp = %d, want 0", got)
	}
}

func TestNormalizeAttachMailPayload(t *testing.T) {
	got, msg := NormalizeAttachMailPayload(AttachMailPayload{
		ID:           " matt_1 ",
		Mail:         " mail_1 ",
		Filename:     " proof.txt ",
		ContentType:  " text/plain ",
		SizeBytes:    42,
		StagedBlobID: " blob_1 ",
	})
	assertMsg(t, "NormalizeAttachMailPayload valid msg", msg, "")
	if got.ID != "matt_1" || got.Mail != "mail_1" || got.Filename != "proof.txt" ||
		got.ContentType != "text/plain" || got.StagedBlobID != "blob_1" || got.SizeBytes != 42 {
		t.Fatalf("NormalizeAttachMailPayload valid = %+v, want trimmed fields", got)
	}
	assertMsg(t, "blank mail", normalizeMsg(NormalizeAttachMailPayload, AttachMailPayload{Mail: " "}), mailRequiredValidationMessage)
	assertMsg(t, "blank filename", normalizeMsg(NormalizeAttachMailPayload, AttachMailPayload{Mail: "mail_1", Filename: " "}), attachmentFilenameRequiredValidationMessage)
	assertMsg(t, "negative size", normalizeMsg(NormalizeAttachMailPayload, AttachMailPayload{Mail: "mail_1", Filename: "proof.txt", SizeBytes: -1}), attachmentSizeValidationMessage)
}

func TestNormalizeAttachPostPayload(t *testing.T) {
	got, msg := NormalizeAttachPostPayload(AttachPostPayload{
		ID:           " att_1 ",
		Post:         " post_1 ",
		Filename:     " proof.txt ",
		ContentType:  " text/plain ",
		SizeBytes:    42,
		StagedBlobID: " blob_1 ",
	})
	assertMsg(t, "NormalizeAttachPostPayload valid msg", msg, "")
	if got.ID != "att_1" || got.Post != "post_1" || got.Filename != "proof.txt" ||
		got.ContentType != "text/plain" || got.StagedBlobID != "blob_1" || got.SizeBytes != 42 {
		t.Fatalf("NormalizeAttachPostPayload valid = %+v, want trimmed fields", got)
	}
	assertMsg(t, "blank post", normalizeMsg(NormalizeAttachPostPayload, AttachPostPayload{Post: " ", Filename: "proof.txt"}), attachPostRequiredValidationMessage)
	assertMsg(t, "blank filename", normalizeMsg(NormalizeAttachPostPayload, AttachPostPayload{Post: "post_1", Filename: " "}), attachPostRequiredValidationMessage)
	assertMsg(t, "negative size", normalizeMsg(NormalizeAttachPostPayload, AttachPostPayload{Post: "post_1", Filename: "proof.txt", SizeBytes: -1}), attachmentSizeValidationMessage)
}
