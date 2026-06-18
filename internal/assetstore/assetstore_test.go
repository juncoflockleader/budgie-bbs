package assetstore

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestS3StoreRoundTrip drives the S3 client against a fake S3 endpoint and
// checks Put/Get/Delete, path-style keying, signing, and PublicBaseURL.
func TestS3StoreRoundTrip(t *testing.T) {
	objects := map[string][]byte{}
	ctypes := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" || r.Header.Get("X-Amz-Content-Sha256") == "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			objects[r.URL.Path] = b
			ctypes[r.URL.Path] = r.Header.Get("Content-Type")
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			b, ok := objects[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", ctypes[r.URL.Path])
			_, _ = w.Write(b)
		case http.MethodDelete:
			delete(objects, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	st, err := NewS3(S3Config{
		Bucket:        "bkt",
		Endpoint:      srv.URL,
		AccessKey:     "ak",
		SecretKey:     "sk",
		PublicBaseURL: "https://cdn.example.com/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if st.PublicBaseURL() != "https://cdn.example.com" {
		t.Fatalf("PublicBaseURL = %q (trailing slash should be trimmed)", st.PublicBaseURL())
	}

	ctx := context.Background()
	payload := []byte("PNGBYTES")
	if err := st.Put(ctx, "site/logo-1.png", "image/png", payload); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, ok := objects["/bkt/site/logo-1.png"]; !ok {
		t.Fatalf("expected path-style key /bkt/site/logo-1.png; have %v", keysOf(objects))
	}
	got, ct, err := st.Get(ctx, "site/logo-1.png")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != string(payload) || ct != "image/png" {
		t.Fatalf("get = %q (%s), want %q (image/png)", got, ct, payload)
	}
	if err := st.Delete(ctx, "site/logo-1.png"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, _, err := st.Get(ctx, "site/logo-1.png"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete err = %v, want ErrNotFound", err)
	}
}

func TestNewS3RequiresCreds(t *testing.T) {
	if _, err := NewS3(S3Config{Bucket: "b"}); err == nil {
		t.Fatal("expected error when access/secret keys are missing")
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
