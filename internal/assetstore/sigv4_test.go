package assetstore

import (
	"net/http"
	"testing"
	"time"
)

// TestSigV4GetVanilla verifies the signing against the canonical AWS
// SigV4 test-suite "get-vanilla" vector, so we know the implementation is
// byte-for-byte correct without a live S3/R2 bucket.
func TestSigV4GetVanilla(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
	signV4(req, "AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "us-east-1", "service", emptyPayloadHash, false, when)

	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, " +
		"SignedHeaders=host;x-amz-date, " +
		"Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("signature mismatch:\n got=%s\nwant=%s", got, want)
	}
}

func TestSigV4SignsContentShaAndType(t *testing.T) {
	req, err := http.NewRequest(http.MethodPut, "https://bucket.example.com/site/logo-1.png", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "image/png")
	body := sha256Hex([]byte("PNGDATA"))
	signV4(req, "ak", "sk", "auto", "s3", body, true, time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC))

	if req.Header.Get("X-Amz-Content-Sha256") != body {
		t.Fatalf("x-amz-content-sha256 = %q, want %q", req.Header.Get("X-Amz-Content-Sha256"), body)
	}
	auth := req.Header.Get("Authorization")
	if !containsAll(auth, "SignedHeaders=content-type;host;x-amz-content-sha256;x-amz-date", "Credential=ak/20240102/auto/s3/aws4_request") {
		t.Fatalf("unexpected authorization for S3 PUT: %s", auth)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}
func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
