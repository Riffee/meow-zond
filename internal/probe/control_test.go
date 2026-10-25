package probe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmptyControlRequires204AndSupportsFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusNoContent)
		case "/unavailable":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()
	for _, tc := range []struct {
		name     string
		controls []Control
		valid    bool
		healthy  bool
	}{
		{"empty 200 is not an expectation", []Control{{URL: server.URL, Status: 200}}, false, false},
		{"expected 204", []Control{{URL: server.URL + "/ok", Status: 204}}, true, true},
		{"unexpected 200", []Control{{URL: server.URL, Status: 204}}, true, false},
		{"independent fallback", []Control{{URL: server.URL + "/unavailable", Status: 204}, {URL: server.URL + "/ok", Status: 204}}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner, err := New(Config{Server: server.URL, ProbeID: "fixture", Token: strings.Repeat("t", 64), Development: true, Controls: tc.controls, Spool: filepath.Join(t.TempDir(), "spool.json")})
			if (err == nil) != tc.valid {
				t.Fatal("unexpected control validation result", err)
			}
			if runner != nil {
				defer runner.Close()
				if runner.Health(context.Background()).Healthy != tc.healthy {
					t.Fatal("unexpected probe health")
				}
			}
		})
	}
}
