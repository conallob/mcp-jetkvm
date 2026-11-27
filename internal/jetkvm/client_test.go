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

func TestSignalingURL(t *testing.T) {
	for _, tc := range []struct{ base, want string }{
		{"http://kvm.local", "ws://kvm.local" + signalingPath},
		{"http://10.0.0.5:8080", "ws://10.0.0.5:8080" + signalingPath},
		{"https://kvm.example.com", "wss://kvm.example.com" + signalingPath},
	} {
		if got := (&Client{baseURL: tc.base}).signalingURL(); got != tc.want {
			t.Errorf("signalingURL(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

func TestExchangeSDPFailureModes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	t.Run("device unreachable is not mistaken for old firmware", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close() // nothing listens any more
		c := &Client{baseURL: url, http: &http.Client{}}
		_, _, err := c.ExchangeSDP(ctx, realOffer(t))
		if err == nil || !strings.Contains(err.Error(), "signaling:") {
			t.Fatalf("expected a signaling dial error, got %v", err)
		}
	})

	for name, tc := range map[string]struct {
		opts fakeDeviceOpts
		want string
	}{
		"socket closes before the greeting":    {fakeDeviceOpts{CloseBeforeGreeting: true}, "waiting for device"},
		"socket closes before the answer":      {fakeDeviceOpts{CloseAfterOffer: true}, "waiting for answer"},
		"answer data is not a string":          {fakeDeviceOpts{AnswerData: ptr("123")}, "parsing SDP answer"},
		"answer string is not valid base64":    {fakeDeviceOpts{AnswerData: quoted("!!!")}, "base64"},
		"offer rejected with an error message": {fakeDeviceOpts{RejectOffer: true}, "rejected"},
	} {
		t.Run(name, func(t *testing.T) {
			_, c := newFakeDevice(t, tc.opts)
			_, _, err := c.ExchangeSDP(ctx, realOffer(t))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected an error containing %q, got %v", tc.want, err)
			}
		})
	}

	t.Run("candidate that arrives before the greeting is kept", func(t *testing.T) {
		_, c := newFakeDevice(t, fakeDeviceOpts{CandidateBeforeGreeting: true})
		_, sig, err := c.ExchangeSDP(ctx, realOffer(t))
		if err != nil {
			t.Fatalf("ExchangeSDP: %v", err)
		}
		defer sig.Close()
		select {
		case cand := <-sig.candidates:
			if cand.Candidate != fakeCandidate {
				t.Fatalf("unexpected first candidate %q", cand.Candidate)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("early candidate was dropped")
		}
	})

	t.Run("unknown and malformed messages are ignored", func(t *testing.T) {
		_, c := newFakeDevice(t, fakeDeviceOpts{Noise: true})
		answer, sig, err := c.ExchangeSDP(ctx, realOffer(t))
		if err != nil {
			t.Fatalf("ExchangeSDP: %v", err)
		}
		defer sig.Close()
		if answer.SDP == "" {
			t.Fatal("expected an answer despite the noise")
		}
		// Only the real candidates may come through, never an empty one.
		deadline := time.After(2 * time.Second)
		for {
			select {
			case cand, ok := <-sig.candidates:
				if !ok {
					return
				}
				if cand.Candidate == "" {
					t.Fatal("an empty candidate got through")
				}
			case <-deadline:
				return
			}
		}
	})
}

// A full candidates buffer must not block the reader once the session is over.
func TestQueueCandidateStopsWhenSessionEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &signaling{ctx: ctx, candidates: make(chan webrtc.ICECandidateInit)} // unbuffered, nobody reading

	done := make(chan struct{})
	go func() {
		s.queueCandidate(signalMessage{
			Type: "new-ice-candidate",
			Data: json.RawMessage(`{"candidate":"` + fakeCandidate + `"}`),
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("queueCandidate blocked on a finished session")
	}
}

func TestExchangeSDPLegacyRequestFailures(t *testing.T) {
	ctx := context.Background()
	offer := &webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: "v=0\r\n"}

	t.Run("request cannot be built", func(t *testing.T) {
		c := &Client{baseURL: "http://bad\x7fhost", http: &http.Client{}}
		if _, err := c.exchangeSDPLegacy(ctx, offer); err == nil {
			t.Fatal("expected an error for an invalid URL")
		}
	})

	t.Run("request cannot be sent", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close()
		c := &Client{baseURL: url, http: &http.Client{}}
		_, err := c.exchangeSDPLegacy(ctx, offer)
		if err == nil || !strings.Contains(err.Error(), "SDP exchange") {
			t.Fatalf("expected an SDP exchange error, got %v", err)
		}
	})
}

func ptr[T any](v T) *T { return &v }

// An answer that parses but that the peer connection rejects must close the
// signaling socket instead of leaking it.
func TestNegotiateBadAnswerClosesSocket(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	garbage, _ := json.Marshal(sdpEnvelope{Type: "answer", SDP: "this is not sdp"})
	_, c := newFakeDevice(t, fakeDeviceOpts{
		AnswerData: quoted(base64.StdEncoding.EncodeToString(garbage)),
	})
	pc, err := newPeerConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()

	err = negotiate(ctx, pc, c)
	if err == nil || !strings.Contains(err.Error(), "set remote description") {
		t.Fatalf("expected a set remote description error, got %v", err)
	}
}
