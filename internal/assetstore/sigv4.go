// Package assetstore provides a pluggable backend for admin-uploaded site
// assets (logo, banner). The default is the app's database; an S3-compatible
// store (AWS S3, Cloudflare R2, MinIO, Backblaze B2…) lets a CDN serve the
// bytes directly so the app is out of the hot path at scale.
package assetstore

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"time"
)

const sigV4Algorithm = "AWS4-HMAC-SHA256"

// emptyPayloadHash is sha256("") — used when a request has no body.
const emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// signV4 signs an HTTP request with AWS Signature Version 4 (header auth),
// mutating req: it sets X-Amz-Date, optionally X-Amz-Content-Sha256, and
// Authorization. hashedPayload is the hex sha256 of the body (use
// emptyPayloadHash for an empty body). When setContentSha is true the
// x-amz-content-sha256 header is added and signed (required by S3).
func signV4(req *http.Request, accessKey, secretKey, region, service, hashedPayload string, setContentSha bool, t time.Time) {
	t = t.UTC()
	amzDate := t.Format("20060102T150405Z")
	dateStamp := t.Format("20060102")

	req.Header.Set("X-Amz-Date", amzDate)
	if setContentSha {
		req.Header.Set("X-Amz-Content-Sha256", hashedPayload)
	}

	host := req.Host
	if host == "" {
		host = req.URL.Host
	}

	// Build the set of signed headers: host + content-type (if present) + any
	// x-amz-* headers. Values are trimmed and folded per SigV4.
	type hv struct{ name, value string }
	headers := []hv{{"host", host}}
	if ct := req.Header.Get("Content-Type"); ct != "" {
		headers = append(headers, hv{"content-type", ct})
	}
	for name, vals := range req.Header {
		ln := strings.ToLower(name)
		if strings.HasPrefix(ln, "x-amz-") {
			headers = append(headers, hv{ln, strings.Join(vals, ",")})
		}
	}
	sort.Slice(headers, func(i, j int) bool { return headers[i].name < headers[j].name })

	var canonHeaders strings.Builder
	signedNames := make([]string, 0, len(headers))
	for _, h := range headers {
		canonHeaders.WriteString(h.name)
		canonHeaders.WriteString(":")
		canonHeaders.WriteString(strings.TrimSpace(collapseSpaces(h.value)))
		canonHeaders.WriteString("\n")
		signedNames = append(signedNames, h.name)
	}
	signedHeaders := strings.Join(signedNames, ";")

	canonicalURI := canonicalURIPath(req.URL.EscapedPath())
	canonicalQuery := canonicalQueryString(req.URL.RawQuery)

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQuery,
		canonHeaders.String(),
		signedHeaders,
		hashedPayload,
	}, "\n")

	scope := strings.Join([]string{dateStamp, region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		sigV4Algorithm,
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	signature := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	auth := sigV4Algorithm +
		" Credential=" + accessKey + "/" + scope +
		", SignedHeaders=" + signedHeaders +
		", Signature=" + signature
	req.Header.Set("Authorization", auth)
}

// collapseSpaces folds runs of spaces into one (SigV4 header value rule).
func collapseSpaces(s string) string {
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return s
}

// canonicalURIPath returns the already-encoded path, or "/" when empty. S3
// keys are encoded by the caller when building the URL.
func canonicalURIPath(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

// canonicalQueryString sorts query parameters by name for the canonical request.
func canonicalQueryString(raw string) string {
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, "&")
	sort.Strings(parts)
	return strings.Join(parts, "&")
}
