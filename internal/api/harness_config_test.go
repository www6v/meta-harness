package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/open-ma/oma-building/internal/harness"
)

func TestHarnessConfigEndpoint_AllEnabled(t *testing.T) {
	r := chi.NewRouter()
	mountHarnessConfigRoutes(r, harness.HarnessState{
		OpenClaw: true,
		Hermes:   true,
		DeepSeek: true,
	})

	req := httptest.NewRequest(http.MethodGet, "/harnesses", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got harness.HarnessState
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !got.OpenClaw || !got.Hermes {
		t.Errorf("expected both enabled, got %+v", got)
	}
}

func TestHarnessConfigEndpoint_OpenClawDisabled(t *testing.T) {
	r := chi.NewRouter()
	mountHarnessConfigRoutes(r, harness.HarnessState{
		OpenClaw: false,
		Hermes:   true,
		DeepSeek: true,
	})

	req := httptest.NewRequest(http.MethodGet, "/harnesses", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var got harness.HarnessState
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.OpenClaw {
		t.Errorf("OpenClaw should be disabled, got %+v", got)
	}
	if !got.Hermes {
		t.Errorf("Hermes should remain enabled, got %+v", got)
	}
}

func TestHarnessConfigEndpoint_BothDisabled(t *testing.T) {
	r := chi.NewRouter()
	mountHarnessConfigRoutes(r, harness.HarnessState{
		OpenClaw: false,
		Hermes:   false,
		DeepSeek: false,
	})

	req := httptest.NewRequest(http.MethodGet, "/harnesses", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var got harness.HarnessState
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.OpenClaw || got.Hermes || got.DeepSeek {
		t.Errorf("all should be disabled, got %+v", got)
	}
}

func TestHarnessConfigEndpoint_DeepSeekOnly(t *testing.T) {
	r := chi.NewRouter()
	mountHarnessConfigRoutes(r, harness.HarnessState{
		OpenClaw: false,
		Hermes:   false,
		DeepSeek: true,
	})

	req := httptest.NewRequest(http.MethodGet, "/harnesses", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var got harness.HarnessState
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.DeepSeek {
		t.Logf("DeepSeek only: %+v", got)
	} else {
		t.Errorf("DeepSeek should be enabled, got %+v", got)
	}
}

func TestHarnessAvailability(t *testing.T) {
	cases := []struct {
		name                                   string
		oc                                     harness.OpenClawConfig
		hc                                     harness.HermesConfig
		ds                                     harness.DeepSeekConfig
		cc                                     harness.CodexConfig
		wantOC, wantHC, wantDS, wantCC         bool
	}{
		{
			name:   "disabled overrides URL",
			oc:     harness.OpenClawConfig{GatewayURL: "http://x", Disabled: true},
			hc:     harness.HermesConfig{GatewayURL: "http://y"},
			ds:     harness.DeepSeekConfig{GatewayURL: "http://z"},
			cc:     harness.CodexConfig{BridgeURL: "http://w"},
			wantOC: false, wantHC: true, wantDS: true, wantCC: true,
		},
		{
			name:   "empty URL counts as disabled",
			oc:     harness.OpenClawConfig{},
			hc:     harness.HermesConfig{},
			ds:     harness.DeepSeekConfig{},
			cc:     harness.CodexConfig{},
			wantOC: false, wantHC: false, wantDS: false, wantCC: false,
		},
		{
			name:   "URL without disabled flag is enabled",
			oc:     harness.OpenClawConfig{GatewayURL: "http://x"},
			hc:     harness.HermesConfig{GatewayURL: "http://y"},
			ds:     harness.DeepSeekConfig{GatewayURL: "http://z"},
			cc:     harness.CodexConfig{BridgeURL: "http://w"},
			wantOC: true, wantHC: true, wantDS: true, wantCC: true,
		},
		{
			name:   "codex disabled overrides URL",
			oc:     harness.OpenClawConfig{},
			hc:     harness.HermesConfig{},
			ds:     harness.DeepSeekConfig{},
			cc:     harness.CodexConfig{BridgeURL: "http://w", Disabled: true},
			wantOC: false, wantHC: false, wantDS: false, wantCC: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := harness.HarnessAvailability(c.oc, c.hc, c.ds, c.cc)
			if got.OpenClaw != c.wantOC || got.Hermes != c.wantHC ||
				got.DeepSeek != c.wantDS || got.Codex != c.wantCC {
				t.Errorf("HarnessAvailability = %+v, want OC=%v HC=%v DS=%v CC=%v",
					got, c.wantOC, c.wantHC, c.wantDS, c.wantCC)
			}
		})
	}
}
