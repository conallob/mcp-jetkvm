package jetkvm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func quoted(s string) *string {
	b, _ := json.Marshal(s)
	q := string(b)
	return &q
}

func TestExchangeSDP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	t.Run("signaling WebSocket", func(t *testing.T) {
		_, c := newFakeDevice(t, fakeDeviceOpts{})
		answer, sig, err := c.ExchangeSDP(ctx, realOffer(t))
		if err != nil {
			t.Fatalf("ExchangeSDP: %v", err)
		}
		if sig == nil {
			t.Fatal("expected a signaling session from a device with a WebSocket")
		}
		defer sig.Close()
		if answer.Type != webrtc.SDPTypeAnswer || answer.SDP == "" {
			t.Fatalf("unexpected answer: %+v", answer)
		}

		// The device trickles candidates after the answer; they must arrive.
		select {
		case cand, ok := <-sig.candidates:
			if !ok {
				t.Fatal("candidates channel closed before any candidate")
			}
			if cand.Candidate == "" {
				t.Fatal("empty candidate")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no trickled candidate received")
		}
	})

	t.Run("legacy HTTP endpoint when there is no WebSocket", func(t *testing.T) {
		_, c := newFakeDevice(t, fakeDeviceOpts{Legacy: true})
		answer, sig, err := c.ExchangeSDP(ctx, realOffer(t))
		if err != nil {
			t.Fatalf("ExchangeSDP: %v", err)
		}
		if sig != nil {
			t.Fatal("legacy exchange should not return a signaling session")
		}
		if answer.Type != webrtc.SDPTypeAnswer || answer.SDP == "" {
			t.Fatalf("unexpected answer: %+v", answer)
		}
	})

	t.Run("device rejects the offer", func(t *testing.T) {
		_, c := newFakeDevice(t, fakeDeviceOpts{RejectOffer: true})
		_, _, err := c.ExchangeSDP(ctx, realOffer(t))
		if err == nil || !strings.Contains(err.Error(), "rejected") {
			t.Fatalf("expected a rejection error, got %v", err)
		}
	})

	t.Run("invalid base64 answer", func(t *testing.T) {
		_, c := newFakeDevice(t, fakeDeviceOpts{AnswerData: quoted("!!!not-base64!!!")})
		if _, _, err := c.ExchangeSDP(ctx, realOffer(t)); err == nil {
			t.Fatal("expected error on invalid base64")
		}
	})

	t.Run("invalid JSON inside base64 answer", func(t *testing.T) {
		bad := base64.StdEncoding.EncodeToString([]byte("not-json"))
		_, c := newFakeDevice(t, fakeDeviceOpts{AnswerData: quoted(bad)})
		if _, _, err := c.ExchangeSDP(ctx, realOffer(t)); err == nil {
			t.Fatal("expected error on invalid inner JSON")
		}
	})

	t.Run("socket without the login cookie is refused", func(t *testing.T) {
		srv, _ := newFakeDevice(t, fakeDeviceOpts{})
		// A client that never logged in carries no authToken cookie.
		jar, _ := cookiejar.New(nil)
		c := &Client{baseURL: srv.URL, http: &http.Client{Jar: jar}}
		_, _, err := c.ExchangeSDP(ctx, realOffer(t))
		if err == nil || !strings.Contains(err.Error(), "401") {
			t.Fatalf("expected an HTTP 401 error, got %v", err)
		}
	})

	t.Run("legacy endpoint errors", func(t *testing.T) {
		for name, handler := range map[string]http.HandlerFunc{
			"HTTP 500": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
			"malformed outer JSON": func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("{bad json"))
			},
		} {
			t.Run(name, func(t *testing.T) {
				mux := http.NewServeMux()
				mux.HandleFunc("/auth/login-local", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
				mux.HandleFunc("/webrtc/session", handler)
				srv := httptest.NewServer(mux)
				defer srv.Close()
				c, err := NewClient(context.Background(), srv.URL, "")
				if err != nil {
					t.Fatalf("NewClient: %v", err)
				}
				if _, _, err := c.ExchangeSDP(ctx, realOffer(t)); err == nil {
					t.Fatal("expected error")
				}
			})
		}
	})
}
