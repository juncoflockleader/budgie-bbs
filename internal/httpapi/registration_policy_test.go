package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/httpapi"
)

func TestSignupPrivacyPolicyAcceptance(t *testing.T) {
	c := newHTTPTestCore(t)
	c.SetPrivacyPolicy(true)
	h := httpapi.New(c, []byte("test-secret")).Handler()

	do := func(method, path string, body any) *httptest.ResponseRecorder {
		var rdr *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rdr = bytes.NewReader(b)
		} else {
			rdr = bytes.NewReader(nil)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, rdr))
		return rec
	}

	// Public policy advertises that acceptance is required, with a version.
	pol := do(http.MethodGet, "/api/v1/auth/policy", nil)
	if pol.Code != http.StatusOK || !bytes.Contains(pol.Body.Bytes(), []byte(`"required":true`)) || !bytes.Contains(pol.Body.Bytes(), []byte("privacyPolicy")) {
		t.Fatalf("policy should advertise required acceptance: %d %s", pol.Code, pol.Body.String())
	}

	// The policy text is served for display.
	pp := do(http.MethodGet, "/api/v1/auth/privacy-policy", nil)
	if pp.Code != http.StatusOK || !bytes.Contains(pp.Body.Bytes(), []byte("Privacy Policy")) || !bytes.Contains(pp.Body.Bytes(), []byte(`"version":"v`)) {
		t.Fatalf("privacy-policy endpoint should serve markdown + version: %d %s", pp.Code, pp.Body.String())
	}

	// Registration without acceptance is rejected.
	r1 := do(http.MethodPost, "/api/v1/auth/register", map[string]any{"name": "alice", "password": "pw12345678"})
	if r1.Code != http.StatusUnprocessableEntity || !bytes.Contains(r1.Body.Bytes(), []byte("policy_acceptance_required")) {
		t.Fatalf("expected policy_acceptance_required, got %d %s", r1.Code, r1.Body.String())
	}

	// Registration with acceptance + intake succeeds and persists the private fields.
	r2 := do(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"name": "alice", "password": "pw12345678",
		"acceptPolicy": true, "policyVersion": "v1",
		"realName": "Alice Liddell", "affiliation": "Wonderland U", "note": "long-time reader",
	})
	if r2.Code != http.StatusCreated {
		t.Fatalf("expected 201 created, got %d %s", r2.Code, r2.Body.String())
	}
	var created struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(r2.Body.Bytes(), &created); err != nil || created.User.ID == "" {
		t.Fatalf("could not parse register response: %v %s", err, r2.Body.String())
	}
	prof, err := c.UserPrivateProfile(created.User.ID)
	if err != nil || prof == nil {
		t.Fatalf("load private profile: %v", err)
	}
	if prof.RealName != "Alice Liddell" || prof.School != "Wonderland U" || prof.ContactNote != "long-time reader" {
		t.Fatalf("intake not persisted: %+v", prof)
	}
}
