package jetkvm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/pion/webrtc/v3"
)

const fakeAuthToken = "fake-auth-token"

// fakeCandidate is a syntactically valid ICE candidate the fake device
// trickles after every answer, so tests have something deterministic to
// observe regardless of the host's network interfaces.
const fakeCandidate = "candidate:1 1 udp 2130706431 192.0.2.1 5000 typ host"

// fakeDeviceOpts selects which firmware behaviour a fakeDevice imitates.
type fakeDeviceOpts struct {
	// Legacy serves only the POST /webrtc/session endpoint of firmware
	// before 0.5.9, with no signaling WebSocket.
	Legacy bool
	// RejectOffer answers every offer with an {"error": ...} message.
	RejectOffer bool
	// AnswerData, when set, is sent as the "data" of the answer message in
	// place of a real answer.
	AnswerData *string
	// OnDevicePC configures the device-side peer connection before the
	// offer is applied (for example to attach data channel handlers).
	OnDevicePC func(*webrtc.PeerConnection)
}

// newFakeDevice starts a test server that speaks the JetKVM firmware's
// login and WebRTC signaling protocol as implemented in jetkvm/kvm web.go:
//
//   - POST /auth/login-local sets the authToken cookie.
//   - GET /webrtc/signaling/client is a WebSocket that requires that cookie,
//     greets with {"type":"device-metadata"}, answers {"type":"offer",
//     "data":{"sd":<b64>}} with {"type":"answer","data":<b64>} straight away,
//     and trickles its own candidates as {"type":"new-ice-candidate"}.
//
// firmware_contract_test.go checks these assumptions against the real source.
func newFakeDevice(t *testing.T, opts fakeDeviceOpts) (*httptest.Server, *Client) {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login-local", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "authToken", Value: fakeAuthToken, Path: "/", HttpOnly: true})
		w.WriteHeader(http.StatusOK)
	})

	if opts.Legacy {
		mux.HandleFunc("/webrtc/session", func(w http.ResponseWriter, r *http.Request) {
			var wire struct {
				SD string `json:"sd"`
			}
			if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
				t.Errorf("decode offer envelope: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			answerSD, _, err := fakeAnswer(t, opts, wire.SD, true)
			if err != nil {
				t.Errorf("fake device: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"sd": answerSD})
		})
	} else {
		mux.HandleFunc(signalingPath, func(w http.ResponseWriter, r *http.Request) {
			if c, err := r.Cookie("authToken"); err != nil || c.Value != fakeAuthToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if err != nil {
				return
			}
			defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
			serveFakeSignaling(t, r.Context(), conn, opts)
		})
	}

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, err := NewClient(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return srv, client
}

func serveFakeSignaling(t *testing.T, ctx context.Context, conn *websocket.Conn, opts fakeDeviceOpts) {
	t.Helper()
	if err := wsjson.Write(ctx, conn, map[string]any{
		"type": "device-metadata",
		"data": map[string]string{"deviceVersion": "0.0.0-test"},
	}); err != nil {
		return
	}
	for {
		var msg struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			return
		}
		if msg.Type != "offer" {
			continue
		}
		if opts.RejectOffer {
			_ = wsjson.Write(ctx, conn, map[string]any{"error": map[string]any{}})
			continue
		}
		var req struct {
			SD string `json:"sd"`
		}
		if err := json.Unmarshal(msg.Data, &req); err != nil {
			t.Errorf("decode offer data: %v", err)
			return
		}
		answerSD, devicePC, err := fakeAnswer(t, opts, req.SD, false)
		if err != nil {
			t.Errorf("fake device: %v", err)
			return
		}
		data := any(answerSD)
		if opts.AnswerData != nil {
			data = json.RawMessage(*opts.AnswerData)
		}
		if err := wsjson.Write(ctx, conn, map[string]any{"type": "answer", "data": data}); err != nil {
			return
		}
		// Like the firmware, trickle candidates after the answer.
		devicePC.OnICECandidate(func(c *webrtc.ICECandidate) {
			if c != nil {
				_ = wsjson.Write(ctx, conn, map[string]any{"type": "new-ice-candidate", "data": c.ToJSON()})
			}
		})
		_ = wsjson.Write(ctx, conn, map[string]any{
			"type": "new-ice-candidate",
			"data": webrtc.ICECandidateInit{Candidate: fakeCandidate},
		})
	}
}

// fakeAnswer applies a base64 offer to a fresh device-side peer connection
// and returns the base64 answer. When waitForGathering is set the answer
// includes the device's candidates, as the legacy endpoint did.
func fakeAnswer(t *testing.T, opts fakeDeviceOpts, offerSD string, waitForGathering bool) (string, *webrtc.PeerConnection, error) {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(offerSD)
	if err != nil {
		return "", nil, err
	}
	var offerEnv sdpEnvelope
	if err := json.Unmarshal(raw, &offerEnv); err != nil {
		return "", nil, err
	}

	devicePC, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return "", nil, err
	}
	t.Cleanup(func() { _ = devicePC.Close() })
	if opts.OnDevicePC != nil {
		opts.OnDevicePC(devicePC)
	}

	if err := devicePC.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offerEnv.SDP}); err != nil {
		return "", nil, err
	}
	answer, err := devicePC.CreateAnswer(nil)
	if err != nil {
		return "", nil, err
	}
	gatherDone := webrtc.GatheringCompletePromise(devicePC)
	if err := devicePC.SetLocalDescription(answer); err != nil {
		return "", nil, err
	}
	if waitForGathering {
		<-gatherDone
	}

	answerRaw, _ := json.Marshal(sdpEnvelope{Type: "answer", SDP: devicePC.LocalDescription().SDP})
	return base64.StdEncoding.EncodeToString(answerRaw), devicePC, nil
}

// realOffer returns a genuine offer from a throwaway peer connection, since
// the fake device parses the SDP it is given.
func realOffer(t *testing.T) *webrtc.SessionDescription {
	t.Helper()
	pc, err := newPeerConnection()
	if err != nil {
		t.Fatalf("newPeerConnection: %v", err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	if _, err := pc.CreateDataChannel("rpc", nil); err != nil {
		t.Fatalf("CreateDataChannel: %v", err)
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}
	gatherDone := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatalf("SetLocalDescription: %v", err)
	}
	<-gatherDone
	return pc.LocalDescription()
}
