package jetkvm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewClientURLHandling(t *testing.T) {
	// Minimal server: accept login, reject everything else.
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login-local", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tests := []struct {
		name string
		host string
	}{
		{"bare host:port", srv.Listener.Addr().String()},
		{"http:// prefix", "http://" + srv.Listener.Addr().String()},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewClient(context.Background(), tc.host, "")
			if err != nil {
				t.Fatalf("NewClient(%q): %v", tc.host, err)
			}
			if c == nil {
				t.Fatal("expected non-nil client")
			}
		})
	}
}

func TestNewClientLoginFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := NewClient(context.Background(), srv.URL, "wrongpassword")
	if err == nil {
		t.Fatal("expected error on login failure, got nil")
	}
}
