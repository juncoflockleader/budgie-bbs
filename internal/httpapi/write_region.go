package httpapi

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/metrics"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

const writeRegionRoutedHeader = "X-Budgie-Write-Region-Routed"

// SetWriteRegionURL configures mutating API requests to be proxied to the
// authoritative write region. Safe read methods continue to use the local
// handler so regional replicas can serve low-latency reads.
func (s *Server) SetWriteRegionURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		s.writeRegionProxy = nil
		s.writeRegionURL = ""
		return nil
	}
	target, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return &url.Error{Op: "parse", URL: raw, Err: errUnsupportedWriteRegionScheme(target.Scheme)}
	}
	if target.Host == "" {
		return &url.Error{Op: "parse", URL: raw, Err: errMissingWriteRegionHost{}}
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	baseDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalHost := req.Host
		baseDirector(req)
		req.Host = target.Host
		req.Header.Set(writeRegionRoutedHeader, "1")
		req.Header.Set("X-Budgie-Write-Region", target.Host)
		if originalHost != "" && req.Header.Get("X-Forwarded-Host") == "" {
			req.Header.Set("X-Forwarded-Host", originalHost)
		}
		if req.Header.Get("X-Forwarded-Proto") == "" {
			req.Header.Set("X-Forwarded-Proto", forwardedProto(req))
		}
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		metrics.WriteRegionProxyFailures.Inc()
		writeError(w, http.StatusBadGateway, proto.ErrWriteRegionUnavailable, "write region is unavailable", true)
	}

	s.writeRegionProxy = proxy
	s.writeRegionURL = raw
	return nil
}

// SetWriteRegionTransport overrides the proxy transport. It is primarily useful
// for tests and for embedding environments that provide their own regional
// routing transport.
func (s *Server) SetWriteRegionTransport(transport http.RoundTripper) {
	if s.writeRegionProxy != nil {
		s.writeRegionProxy.Transport = transport
	}
}

func (s *Server) routeWriteRegion(next http.Handler) http.Handler {
	if s.writeRegionProxy == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if shouldRouteToWriteRegion(r) {
			metrics.WriteRegionRoutedRequests.Inc(map[string]string{"method": r.Method})
			s.writeRegionProxy.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func shouldRouteToWriteRegion(r *http.Request) bool {
	if r == nil || !strings.HasPrefix(r.URL.Path, "/api/v1/") {
		return false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

func forwardedProto(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

type errUnsupportedWriteRegionScheme string

func (e errUnsupportedWriteRegionScheme) Error() string {
	if e == "" {
		return "write region URL scheme is required"
	}
	return "unsupported write region URL scheme: " + string(e)
}

type errMissingWriteRegionHost struct{}

func (errMissingWriteRegionHost) Error() string {
	return "write region URL host is required"
}
