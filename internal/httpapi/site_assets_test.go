package httpapi_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func siteAssetReq(handler http.Handler, method, path, token string, body []byte, ct string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, path, bytes.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if ct != "" {
		r.Header.Set("Content-Type", ct)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	return rec
}

func TestHTTPSiteAssetUploadDownloadDelete(t *testing.T) {
	_, handler := setupHTTPTestServer(t)
	adminToken := registerUser(t, handler, "admin") // first user is admin
	userToken := registerUser(t, handler, "bob")

	png := append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, []byte("fake-png-body")...)

	if rec := siteAssetReq(handler, http.MethodGet, "/api/v1/site/asset/logo", "", nil, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unset logo GET = %d, want 404", rec.Code)
	}
	if rec := siteAssetReq(handler, http.MethodPost, "/api/v1/admin/site-asset/logo", userToken, png, "image/png"); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin upload = %d, want 403", rec.Code)
	}
	if rec := siteAssetReq(handler, http.MethodPost, "/api/v1/admin/site-asset/logo", adminToken, []byte("not a png at all"), "image/png"); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("non-PNG upload = %d, want 422", rec.Code)
	}
	if rec := siteAssetReq(handler, http.MethodPost, "/api/v1/admin/site-asset/bogus", adminToken, png, "image/png"); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown asset slot = %d, want 422", rec.Code)
	}
	if rec := siteAssetReq(handler, http.MethodPost, "/api/v1/admin/site-asset/logo", adminToken, png, "image/png"); rec.Code != http.StatusOK {
		t.Fatalf("admin PNG upload = %d, want 200", rec.Code)
	}

	rec := siteAssetReq(handler, http.MethodGet, "/api/v1/site/asset/logo", "", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("logo GET after upload = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type = %q, want image/png", ct)
	}
	if !bytes.Equal(rec.Body.Bytes(), png) {
		t.Fatalf("downloaded bytes do not match uploaded bytes")
	}

	if rec := siteAssetReq(handler, http.MethodDelete, "/api/v1/admin/site-asset/logo", adminToken, nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("admin delete = %d, want 200", rec.Code)
	}
	if rec := siteAssetReq(handler, http.MethodGet, "/api/v1/site/asset/logo", "", nil, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("logo GET after delete = %d, want 404", rec.Code)
	}
}
