package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/httpapi"
)

func TestQueryTokenOnlyAcceptedOnStreamRoutes(t *testing.T) {
	c := newHTTPTestCore(t)
	h := httpapi.New(c, []byte("test-secret")).Handler()

	reg := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader([]byte(`{"name":"alice","password":"password123"}`)))
	regRec := httptest.NewRecorder()
	h.ServeHTTP(regRec, reg)
	var body map[string]any
	_ = json.Unmarshal(regRec.Body.Bytes(), &body)
	token, _ := body["token"].(string)
	if token == "" {
		t.Fatalf("no token from register: %s", regRec.Body.String())
	}

	get := func(path string) int {
		req := httptest.NewRequest(http.MethodGet, path+"?token="+token, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// A non-stream route must NOT accept the token via query param.
	if code := get("/api/v1/account/2fa"); code != http.StatusUnauthorized {
		t.Fatalf("non-stream route should reject ?token=, got %d", code)
	}
	// The SSE event routes must accept it (EventSource can't set headers).
	if code := get("/api/v1/events"); code == http.StatusUnauthorized {
		t.Fatalf("stream route should accept ?token=, got %d", code)
	}
}
