package api

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/open-ma/oma-building/internal/harness"
)

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// dshProxyDeps holds dependencies for the DeepSeek Harness API proxy.
type dshProxyDeps struct {
	// HarnessURL is the base URL of the deepseek-harness gateway.
	HarnessURL string
}

// mountDshProxyRoutes registers a catch-all proxy for /api/* requests that
// aren't handled by more specific routes (like /api/workflows/*). The console
// frontend calls deepseek-harness RPC endpoints directly (e.g.,
// /api/fileUploads/upload, /api/workspaceFiles/list), and the proxy adds
// the authentication cookie by reading the deepseek-harness credentials file.
func mountDshProxyRoutes(r chi.Router, deps dshProxyDeps) {
	if strings.TrimSpace(deps.HarnessURL) == "" {
		return
	}

	target, err := url.Parse(strings.TrimRight(deps.HarnessURL, "/"))
	if err != nil {
		log.Printf("warning: dsh proxy: invalid harness URL %q: %v", deps.HarnessURL, err)
		return
	}

	// Extract authority (host:port) from the target URL for auth cookie
	authority := target.Host

	// Try to load auth cookie from deepseek-harness credentials file
	authCookie, err := harness.LoadDshAuthCookieForTest(authority)
	if err != nil {
		log.Printf("[DSH PROXY] warning: cannot load auth cookie: %v", err)
		log.Printf("[DSH PROXY] Requests to gateway will require browser auth cookies")
	} else {
		log.Printf("[DSH PROXY] loaded auth cookie for authority %s (name=%s)", authority, authCookie.Name)
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// Set the target URL scheme and host
			pr.SetURL(target)
			// Set the Host header to match the gateway (required by trust fence)
			// The gateway checks that Host is a loopback address
			pr.Out.Host = target.Host
			pr.Out.Header.Set("Host", target.Host)
			// Do NOT set Origin header - let the gateway use Host fence only
			// Setting Origin can cause issues if it doesn't match exactly
			// Remove Origin if present (from browser requests)
			pr.Out.Header.Del("Origin")
			// Add auth cookie if available
			if authCookie != nil {
				// Preserve existing cookies and add our auth cookie
				if existing := pr.Out.Header.Get("Cookie"); existing != "" {
					pr.Out.Header.Set("Cookie", existing+"; "+authCookie.Name+"="+authCookie.Value)
				} else {
					pr.Out.Header.Set("Cookie", authCookie.Name+"="+authCookie.Value)
				}
				log.Printf("[DSH PROXY] Request to %s, Cookie: %s=%s (length=%d)",
					pr.Out.URL.Path, authCookie.Name, authCookie.Value[:min(20, len(authCookie.Value))], len(authCookie.Value))
			} else {
				log.Printf("[DSH PROXY] Request to %s, NO auth cookie available", pr.Out.URL.Path)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			log.Printf("[DSH PROXY] error: %v", err)
			http.Error(
				w,
				"deepseek harness unavailable: "+err.Error(),
				http.StatusBadGateway,
			)
		},
	}

	// Handle all /api/* requests by forwarding to the harness gateway.
	// More specific routes (e.g., /api/workflows/*) take precedence.
	r.Handle("/api", proxy)
	r.Handle("/api/*", proxy)
}
