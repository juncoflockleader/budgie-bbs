package assetstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrNotFound is returned by Store.Get when the object does not exist.
var ErrNotFound = errors.New("assetstore: object not found")

// Store is a pluggable byte backend for site assets. The app keeps asset
// metadata in its database; when a Store is configured the bytes live here
// (e.g. an S3/R2 bucket) and PublicBaseURL points clients/CDN at them directly.
type Store interface {
	Put(ctx context.Context, key, contentType string, data []byte) error
	Get(ctx context.Context, key string) (data []byte, contentType string, err error)
	Delete(ctx context.Context, key string) error
	// PublicBaseURL is the externally reachable base (CDN or bucket) under which
	// keys are served, with no trailing slash; "" if the app must serve bytes.
	PublicBaseURL() string
}

// S3Config configures an S3-compatible backend (AWS S3, Cloudflare R2, MinIO,
// Backblaze B2…). For R2 set Endpoint to https://<account>.r2.cloudflarestorage.com,
// Region "auto", PathStyle true. For AWS S3 leave Endpoint empty.
type S3Config struct {
	Bucket        string
	Region        string
	Endpoint      string // empty = AWS virtual-host style from Region
	AccessKey     string
	SecretKey     string
	PublicBaseURL string // CDN/base URL clients use to read objects
	PathStyle     bool   // bucket in the path (R2/MinIO) vs virtual-host (AWS)
}

// S3Store is a dependency-free S3-compatible client (SigV4 header auth).
type S3Store struct {
	cfg    S3Config
	client *http.Client
}

var _ Store = (*S3Store)(nil)

// NewS3 validates the config and returns an S3-backed Store.
func NewS3(cfg S3Config) (*S3Store, error) {
	cfg.Bucket = strings.TrimSpace(cfg.Bucket)
	cfg.AccessKey = strings.TrimSpace(cfg.AccessKey)
	cfg.SecretKey = strings.TrimSpace(cfg.SecretKey)
	cfg.Endpoint = strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	cfg.PublicBaseURL = strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/")
	if cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("assetstore s3: bucket, access key and secret key are required")
	}
	if strings.TrimSpace(cfg.Region) == "" {
		cfg.Region = "auto"
	}
	if cfg.Endpoint != "" {
		cfg.PathStyle = true // custom endpoints (R2/MinIO) use path-style buckets
	}
	return &S3Store{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}, nil
}

func (s *S3Store) PublicBaseURL() string { return s.cfg.PublicBaseURL }

func (s *S3Store) objectURL(key string) string {
	key = strings.TrimPrefix(key, "/")
	if s.cfg.Endpoint != "" {
		if s.cfg.PathStyle {
			return s.cfg.Endpoint + "/" + s.cfg.Bucket + "/" + key
		}
		return s.cfg.Endpoint + "/" + key
	}
	return "https://" + s.cfg.Bucket + ".s3." + s.cfg.Region + ".amazonaws.com/" + key
}

func (s *S3Store) sign(req *http.Request, payloadHash string) {
	signV4(req, s.cfg.AccessKey, s.cfg.SecretKey, s.cfg.Region, "s3", payloadHash, true, time.Now())
}

func (s *S3Store) Put(ctx context.Context, key, contentType string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.objectURL(key), bytes.NewReader(data))
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.ContentLength = int64(len(data))
	s.sign(req, sha256Hex(data))
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return s3Error("put", resp)
	}
	return nil
}

func (s *S3Store) Get(ctx context.Context, key string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.objectURL(key), nil)
	if err != nil {
		return nil, "", err
	}
	s.sign(req, emptyPayloadHash)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", ErrNotFound
	}
	if resp.StatusCode/100 != 2 {
		return nil, "", s3Error("get", resp)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return body, resp.Header.Get("Content-Type"), nil
}

func (s *S3Store) Delete(ctx context.Context, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.objectURL(key), nil)
	if err != nil {
		return err
	}
	s.sign(req, emptyPayloadHash)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusNotFound {
		return s3Error("delete", resp)
	}
	return nil
}

func s3Error(op string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	msg := strings.TrimSpace(string(body))
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return fmt.Errorf("assetstore s3 %s: status %d: %s", op, resp.StatusCode, msg)
}
