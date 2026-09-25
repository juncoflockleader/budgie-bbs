package httpapi_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/httpapi"
)

func TestRequestBodyIsSizeLimited(t *testing.T) {
	c := newHTTPTestCore(t)
	h := httpapi.New(c, []byte("test-secret")).Handler()

	// A JSON body well over the cap is rejected rather than buffered whole.
	huge := `{"name":"x","password":"` + strings.Repeat("a", 5<<20) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader([]byte(huge)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusCreated || rec.Code == http.StatusAccepted {
		t.Fatalf("oversized register body should be rejected, got %d", rec.Code)
	}

	// A normal-sized registration still succeeds (cap doesn't break legit use).
	ok := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader([]byte(`{"name":"alice","password":"password123"}`)))
	okRec := httptest.NewRecorder()
	h.ServeHTTP(okRec, ok)
	if okRec.Code != http.StatusCreated {
		t.Fatalf("normal register should succeed, got %d %s", okRec.Code, okRec.Body.String())
	}
}
