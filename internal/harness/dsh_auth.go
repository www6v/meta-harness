package harness

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// dshAuthCookie holds the constructed authentication cookie for dsh web API.
// Exported as DshAuthCookie for use in other packages.
type dshAuthCookie struct {
	Name  string
	Value string
}

// DshAuthCookie is the exported type for authentication cookie.
type DshAuthCookie = dshAuthCookie

// dshCookiePayload is the payload encoded in the authentication cookie
type dshCookiePayload struct {
	Version   int    `json:"version"`
	Authority string `json:"authority"`
	IssuedAt  int64  `json:"issuedAt"`
	ExpiresAt int64  `json:"expiresAt"`
}

const (
	dshCookiePrefix       = "dsh-auth-"
	dshCookieVersion      = 1
	dshCookieMaxAgeDays   = 7
	dshBrowserSessionKey  = "client-connection/browser-session"
)

// LoadDshAuthCookieForTest reads the dsh credentials file and constructs a valid
// authentication cookie for the given authority (e.g., "127.0.0.1:3080").
// This is exported for testing purposes.
func LoadDshAuthCookieForTest(authority string) (*dshAuthCookie, error) {
	return loadDshAuthCookie(authority)
}

// loadDshAuthCookie reads the dsh credentials file and constructs a valid
// authentication cookie for the given authority (e.g., "127.0.0.1:3080").
func loadDshAuthCookie(authority string) (*dshAuthCookie, error) {
	// Find the credentials file
	dshHome := os.Getenv("DSH_HOME")
	if dshHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Printf("[DSH AUTH] cannot determine home directory: %v", err)
			return nil, fmt.Errorf("dsh auth: cannot determine home directory: %w", err)
		}
		dshHome = filepath.Join(home, ".dsh")
	}
	credPath := filepath.Join(dshHome, ".credentials.yaml")
	log.Printf("[DSH AUTH] loading credentials from %s for authority %s", credPath, authority)

	// Parse the credentials file manually to avoid external dependencies
	secret, err := parseBrowserSessionSecret(credPath)
	if err != nil {
		return nil, err
	}

	// Decode the base64url-encoded secret
	secretBytes, err := base64URLDecode(secret)
	if err != nil {
		return nil, fmt.Errorf("dsh auth: cannot decode secret: %w", err)
	}

	// Construct the cookie
	now := time.Now()
	cookiePayload := dshCookiePayload{
		Version:   dshCookieVersion,
		Authority: authority,
		IssuedAt:  now.UnixMilli(),
		ExpiresAt: now.AddDate(0, 0, dshCookieMaxAgeDays).UnixMilli(),
	}

	// Encode the cookie value: v1.<base64url(json)>.<base64url(hmac-sha256)>
	payloadBytes, err := json.Marshal(cookiePayload)
	if err != nil {
		return nil, fmt.Errorf("dsh auth: cannot marshal cookie payload: %w", err)
	}
	body := base64URLEncode(payloadBytes)
	signature := base64URLEncode(hmacSHA256(secretBytes, []byte(body)))
	cookieValue := fmt.Sprintf("v1.%s.%s", body, signature)

	// Cookie name: dsh-auth-<base64url(sha256(authority))>
	cookieName := dshCookiePrefix + base64URLEncode(sha256Sum([]byte(authority)))

	log.Printf("[DSH AUTH] successfully loaded auth cookie: name=%s, value_length=%d", cookieName, len(cookieValue))

	return &dshAuthCookie{
		Name:  cookieName,
		Value: cookieValue,
	}, nil
}

// parseBrowserSessionSecret extracts the secret from the browser-session record
// in the credentials YAML file. This is a simple parser that handles the
// specific structure we need without requiring a full YAML library.
func parseBrowserSessionSecret(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("dsh auth: cannot open credentials file %s: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var inBrowserSession bool
	var inPayload bool
	var inSecret bool

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Check if we're entering the browser-session record
		if strings.HasPrefix(trimmed, dshBrowserSessionKey+":") {
			inBrowserSession = true
			continue
		}

		// If we're in browser-session, look for payload and secret
		if inBrowserSession {
			// Check if we've left the browser-session block (new top-level key)
			if len(line) > 0 && line[0] != ' ' && line[0] != '\t' && strings.Contains(line, ":") {
				inBrowserSession = false
				inPayload = false
				inSecret = false
				continue
			}

			if strings.HasPrefix(trimmed, "payload:") {
				inPayload = true
				continue
			}

			if inPayload && strings.HasPrefix(trimmed, "secret:") {
				// Extract the secret value
				parts := strings.SplitN(trimmed, ":", 2)
				if len(parts) == 2 {
					return strings.TrimSpace(parts[1]), nil
				}
				inSecret = true
				continue
			}

			// Handle multi-line secret (if it's on the next line)
			if inSecret && trimmed != "" {
				return trimmed, nil
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("dsh auth: error reading credentials file: %w", err)
	}

	return "", fmt.Errorf("dsh auth: browser-session secret not found in %s", path)
}

// base64URLEncode encodes bytes to base64url without padding
func base64URLEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

// base64URLDecode decodes a base64url string (with or without padding)
func base64URLDecode(s string) ([]byte, error) {
	// Add padding if necessary
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}

// hmacSHA256 computes HMAC-SHA256
func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// sha256Sum computes SHA256 hash
func sha256Sum(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}

// extractAuthority extracts the authority (host:port) from a URL
func extractAuthority(rawURL string) string {
	// Remove protocol prefix
	url := rawURL
	if idx := strings.Index(url, "://"); idx != -1 {
		url = url[idx+3:]
	}
	// Remove path
	if idx := strings.Index(url, "/"); idx != -1 {
		url = url[:idx]
	}
	return url
}
