package captcha

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRandomCodeUnambiguous(t *testing.T) {
	for i := 0; i < 200; i++ {
		c := RandomCode(5)
		if len(c) != 5 {
			t.Fatalf("len = %d, want 5", len(c))
		}
		if strings.ContainsAny(c, "01OIL") {
			t.Fatalf("code %q contains ambiguous characters", c)
		}
	}
}

func TestHashAnswerCaseAndChallengeBound(t *testing.T) {
	secret := "s3cr3t"
	h := HashAnswer(secret, "chal_1", "ABcd2")
	// Case/whitespace-insensitive.
	if !AnswerMatches(secret, "chal_1", "  abCD2 ", h) {
		t.Fatal("expected normalized answer to match")
	}
	// Wrong answer fails.
	if AnswerMatches(secret, "chal_1", "ABcd3", h) {
		t.Fatal("wrong answer matched")
	}
	// Same answer, different challenge id must not match (replay protection).
	if AnswerMatches(secret, "chal_2", "ABcd2", h) {
		t.Fatal("hash replayed across challenge ids")
	}
	// Different secret must not match.
	if AnswerMatches("other", "chal_1", "ABcd2", h) {
		t.Fatal("hash matched under a different secret")
	}
}

func TestRenderSVGContainsCode(t *testing.T) {
	svg := RenderSVG("AB2CD")
	if !strings.HasPrefix(svg, "<svg") || !strings.HasSuffix(svg, "</svg>") {
		t.Fatalf("not a standalone svg: %.40s", svg)
	}
	for _, ch := range "AB2CD" {
		if !strings.Contains(svg, ">"+string(ch)+"</text>") {
			t.Fatalf("svg missing character %q", string(ch))
		}
	}
}

func TestDefaultVerifyURL(t *testing.T) {
	if DefaultVerifyURL("turnstile") == "" || DefaultVerifyURL("hcaptcha") == "" || DefaultVerifyURL("recaptcha") == "" {
		t.Fatal("known providers should have default verify URLs")
	}
	if DefaultVerifyURL("unknown") != "" {
		t.Fatal("unknown provider should have no default URL")
	}
}

func TestProviderVerify(t *testing.T) {
	var gotSecret, gotResponse string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotSecret = r.PostFormValue("secret")
		gotResponse = r.PostFormValue("response")
		if gotResponse == "good-token" {
			_, _ = w.Write([]byte(`{"success":true}`))
		} else {
			_, _ = w.Write([]byte(`{"success":false,"error-codes":["invalid-input-response"]}`))
		}
	}))
	defer srv.Close()

	v := NewProviderVerifier(ProviderConfig{VerifyURL: srv.URL, Secret: "sk"}, srv.Client())

	ok, err := v.Verify(context.Background(), "good-token", "1.2.3.4")
	if err != nil || !ok {
		t.Fatalf("good token: ok=%v err=%v", ok, err)
	}
	if gotSecret != "sk" || gotResponse != "good-token" {
		t.Fatalf("provider got secret=%q response=%q", gotSecret, gotResponse)
	}

	ok, err = v.Verify(context.Background(), "bad-token", "")
	if err != nil || ok {
		t.Fatalf("bad token should fail cleanly: ok=%v err=%v", ok, err)
	}

	// Empty token short-circuits to false without an HTTP call.
	ok, _ = v.Verify(context.Background(), "  ", "")
	if ok {
		t.Fatal("empty token should not verify")
	}
}

func TestProviderVerifyMisconfigured(t *testing.T) {
	v := NewProviderVerifier(ProviderConfig{}, nil)
	if _, err := v.Verify(context.Background(), "x", ""); err == nil {
		t.Fatal("expected error when secret/url missing")
	}
}
