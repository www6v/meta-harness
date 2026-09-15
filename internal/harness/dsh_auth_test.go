package harness

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDshAuthCookie(t *testing.T) {
	// Create a temporary credentials file
	tmpDir := t.TempDir()
	credPath := filepath.Join(tmpDir, ".credentials.yaml")

	// Write a test credentials file
	credContent := `version: 1
records:
  client-connection/browser-session:
    kind: grant
    payload:
      version: 1
      secret: BWfO8bVG4AFqD61xF2vA0-g-lukRmiJnz__gvvqXSb0
refs:
  DEEPSEEK_API_KEY: sk-test123
`
	if err := os.WriteFile(credPath, []byte(credContent), 0600); err != nil {
		t.Fatalf("failed to write test credentials: %v", err)
	}

	// Set DSH_HOME to the temp directory
	oldDshHome := os.Getenv("DSH_HOME")
	os.Setenv("DSH_HOME", tmpDir)
	defer os.Setenv("DSH_HOME", oldDshHome)

	// Test loading the cookie
	cookie, err := loadDshAuthCookie("127.0.0.1:3080")
	if err != nil {
		t.Fatalf("loadDshAuthCookie failed: %v", err)
	}

	// Verify the cookie was created
	if cookie == nil {
		t.Fatal("cookie is nil")
	}

	// Verify the cookie name format
	expectedPrefix := "dsh-auth-"
	if len(cookie.Name) <= len(expectedPrefix) {
		t.Errorf("cookie name %q is too short", cookie.Name)
	}
	if cookie.Name[:len(expectedPrefix)] != expectedPrefix {
		t.Errorf("cookie name %q doesn't start with %q", cookie.Name, expectedPrefix)
	}

	// Verify the cookie value format (v1.<body>.<signature>)
	if len(cookie.Value) == 0 {
		t.Error("cookie value is empty")
	}
	if cookie.Value[:3] != "v1." {
		t.Errorf("cookie value %q doesn't start with v1.", cookie.Value)
	}

	t.Logf("Cookie name: %s", cookie.Name)
	t.Logf("Cookie value: %s", cookie.Value)
}

func TestExtractAuthority(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"http://127.0.0.1:3080", "127.0.0.1:3080"},
		{"https://example.com:8080", "example.com:8080"},
		{"http://localhost", "localhost"},
		{"127.0.0.1:3080", "127.0.0.1:3080"},
		{"http://127.0.0.1:3080/api/session.create", "127.0.0.1:3080"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := extractAuthority(tt.input)
			if result != tt.expected {
				t.Errorf("extractAuthority(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestBase64URL(t *testing.T) {
	// Test round-trip encoding/decoding
	original := []byte("test data 123")
	encoded := base64URLEncode(original)
	decoded, err := base64URLDecode(encoded)
	if err != nil {
		t.Fatalf("base64URLDecode failed: %v", err)
	}
	if string(decoded) != string(original) {
		t.Errorf("round-trip failed: got %q, want %q", decoded, original)
	}
}
