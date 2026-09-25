package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/httpapi"
)

func TestLoginRateLimitLocksOut(t *testing.T) {
	c := newHTTPTestCore(t)
	h := httpapi.New(c, []byte("test-secret")).Handler()

	do := func(body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(b))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// Register the target account.
	reg := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader([]byte(`{"name":"victim","password":"password123"}`)))
	regRec := httptest.NewRecorder()
	h.ServeHTTP(regRec, reg)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", regRec.Code, regRec.Body.String())
	}

	// The limiter threshold is 10 failures; the 11th attempt must be throttled.
	got429 := false
	for i := 0; i < 15; i++ {
		rec := do(map[string]string{"name": "victim", "password": "wrong-password"})
		if rec.Code == http.StatusTooManyRequests {
			got429 = true
			if rec.Header().Get("Retry-After") == "" {
				t.Fatal("expected a Retry-After header on a 429")
			}
			break
		}
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401 before lockout, got %d %s", i, rec.Code, rec.Body.String())
		}
	}
	if !got429 {
		t.Fatal("expected the login endpoint to rate-limit after repeated failures")
	}

	// Even the correct password is refused while locked out (no oracle).
	if rec := do(map[string]string{"name": "victim", "password": "password123"}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected correct password to be throttled during lockout, got %d", rec.Code)
	}
}
