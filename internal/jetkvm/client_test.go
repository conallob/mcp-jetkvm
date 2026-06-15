package jetkvm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pion/webrtc/v3"
)

func TestNewClientURLHandling(t *testing.T) {
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

// sdpServer builds a test server that handles login and returns a synthetic SDP answer.
func sdpServer(t *testing.T, sdpHandler http.HandlerFunc) (*httptest.Server, *Client) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login-local", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webrtc/session", sdpHandler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c, err := NewClient(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return srv, c
}

func encodedSDP(sdpType, sdp string) string {
	raw, _ := json.Marshal(sdpEnvelope{Type: sdpType, SDP: sdp})
	return base64.StdEncoding.EncodeToString(raw)
}

func TestExchangeSDP(t *testing.T) {
	offer := &webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  "v=0\r\n",
	}

	t.Run("success", func(t *testing.T) {
		_, c := sdpServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"sd": encodedSDP("answer", "v=0\r\nanswer\r\n"),
			})
		})
		answer, err := c.ExchangeSDP(context.Background(), offer)
		if err != nil {
			t.Fatalf("ExchangeSDP: %v", err)
		}
		if answer.Type != webrtc.SDPTypeAnswer {
			t.Fatalf("expected answer type, got %v", answer.Type)
		}
		if answer.SDP != "v=0\r\nanswer\r\n" {
			t.Fatalf("unexpected SDP: %q", answer.SDP)
		}
	})

	t.Run("HTTP error", func(t *testing.T) {
		_, c := sdpServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		_, err := c.ExchangeSDP(context.Background(), offer)
		if err == nil {
			t.Fatal("expected error on HTTP 500")
		}
	})

	t.Run("invalid base64 in response", func(t *testing.T) {
		_, c := sdpServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"sd": "!!!not-base64!!!"})
		})
		_, err := c.ExchangeSDP(context.Background(), offer)
		if err == nil {
			t.Fatal("expected error on invalid base64")
		}
	})

	t.Run("invalid JSON inside base64", func(t *testing.T) {
		_, c := sdpServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			bad := base64.StdEncoding.EncodeToString([]byte("not-json"))
			json.NewEncoder(w).Encode(map[string]string{"sd": bad})
		})
		_, err := c.ExchangeSDP(context.Background(), offer)
		if err == nil {
			t.Fatal("expected error on invalid inner JSON")
		}
	})

	t.Run("malformed outer JSON", func(t *testing.T) {
		_, c := sdpServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("{bad json"))
		})
		_, err := c.ExchangeSDP(context.Background(), offer)
		if err == nil {
			t.Fatal("expected error on malformed outer JSON")
		}
	})
}
