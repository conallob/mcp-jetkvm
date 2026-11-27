package jetkvm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/pion/webrtc/v3"
)

// signalingPath is the WebSocket endpoint the device uses for WebRTC
// signaling. It replaced the one-shot POST /webrtc/session endpoint in
// firmware 0.5.9 (jetkvm/kvm#1569).
const signalingPath = "/webrtc/signaling/client"

// legacySessionPath is the one-shot HTTP SDP exchange that firmware before
// 0.5.9 offers. It is only a fallback, used when signalingPath is a 404.
const legacySessionPath = "/webrtc/session"

// Client handles authenticated HTTP communication with a JetKVM device.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient creates an authenticated client for the given host.
// password may be empty if the device is in noPassword mode.
func NewClient(ctx context.Context, host, password string) (*Client, error) {
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}
	jar, _ := cookiejar.New(nil)
	c := &Client{
		baseURL: host,
		http:    &http.Client{Jar: jar},
	}
	if err := c.login(ctx, password); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) login(ctx context.Context, password string) error {
	body, _ := json.Marshal(map[string]string{"password": password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/auth/login-local", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("login failed: HTTP %d", resp.StatusCode)
	}
	return nil
}

type sdpEnvelope struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

// encodeSDP returns the device's wire format for a session description:
// base64(JSON({type,sdp})).
func encodeSDP(desc *webrtc.SessionDescription) string {
	raw, _ := json.Marshal(sdpEnvelope{Type: desc.Type.String(), SDP: desc.SDP}) // two strings: cannot fail
	return base64.StdEncoding.EncodeToString(raw)
}

func decodeAnswer(b64 string) (*webrtc.SessionDescription, error) {
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("base64 SDP answer: %w", err)
	}
	var answer sdpEnvelope
	if err := json.Unmarshal(decoded, &answer); err != nil {
		return nil, fmt.Errorf("parsing SDP answer: %w", err)
	}
	return &webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.SDP}, nil
}

// signalMessage is the envelope for every message on the signaling socket.
// An error reply from the device has no type, only an "error" field.
type signalMessage struct {
	Type  string          `json:"type"`
	Data  json.RawMessage `json:"data"`
	Error json.RawMessage `json:"error"`
}

// signaling is an open signaling WebSocket for one WebRTC session. The device
// answers an offer immediately and then trickles its own ICE candidates over
// this socket, so it has to stay open while the session is in use.
type signaling struct {
	conn       *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	candidates chan webrtc.ICECandidateInit
}

// Close ends the signaling socket.
func (s *signaling) Close() {
	s.cancel()
	_ = s.conn.Close(websocket.StatusNormalClosure, "")
}

// errSignalingUnsupported means the device has no signaling WebSocket, which
// is the case for firmware older than 0.5.9.
var errSignalingUnsupported = errors.New("device has no signaling WebSocket")

func (c *Client) signalingURL() string {
	switch {
	case strings.HasPrefix(c.baseURL, "https://"):
		return "wss://" + strings.TrimPrefix(c.baseURL, "https://") + signalingPath
	default:
		return "ws://" + strings.TrimPrefix(c.baseURL, "http://") + signalingPath
	}
}

func (c *Client) dialSignaling(ctx context.Context) (*signaling, error) {
	// The login cookie rides along because the dial goes through c.http and
	// its cookie jar. The device rejects the socket without it.
	conn, resp, err := websocket.Dial(ctx, c.signalingURL(), &websocket.DialOptions{HTTPClient: c.http})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, errSignalingUnsupported
		}
		if resp != nil {
			return nil, fmt.Errorf("signaling: HTTP %d: %w", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("signaling: %w", err)
	}
	conn.SetReadLimit(1 << 20)
	sctx, cancel := context.WithCancel(ctx)
	return &signaling{
		conn:       conn,
		ctx:        sctx,
		cancel:     cancel,
		candidates: make(chan webrtc.ICECandidateInit, 128),
	}, nil
}

// readMessage reads one signaling message.
func (s *signaling) readMessage(ctx context.Context) (signalMessage, error) {
	var msg signalMessage
	if err := wsjson.Read(ctx, s.conn, &msg); err != nil {
		return msg, err
	}
	return msg, nil
}

// exchange waits for the device's greeting, sends the offer, and reads until
// the answer arrives. Candidates that arrive before the answer are queued.
func (s *signaling) exchange(ctx context.Context, offer *webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	// The browser sends its offer only after the device-metadata greeting, so
	// do the same. Reads must use a context that outlives the call: coder/websocket
	// closes the connection when a read's context is cancelled.
	for {
		msg, err := s.readMessage(ctx)
		if err != nil {
			return nil, fmt.Errorf("signaling: waiting for device: %w", err)
		}
		if msg.Type == "device-metadata" {
			break
		}
		s.queueCandidate(msg)
	}

	if err := wsjson.Write(ctx, s.conn, map[string]any{
		"type": "offer",
		"data": map[string]string{"sd": encodeSDP(offer)},
	}); err != nil {
		return nil, fmt.Errorf("signaling: sending offer: %w", err)
	}

	for {
		msg, err := s.readMessage(ctx)
		if err != nil {
			return nil, fmt.Errorf("signaling: waiting for answer: %w", err)
		}
		switch {
		case msg.Type == "answer":
			var b64 string
			if err := json.Unmarshal(msg.Data, &b64); err != nil {
				return nil, fmt.Errorf("parsing SDP answer: %w", err)
			}
			return decodeAnswer(b64)
		case msg.Type == "" && len(msg.Error) > 0:
			return nil, fmt.Errorf("device rejected offer: %s", msg.Error)
		default:
			s.queueCandidate(msg)
		}
	}
}

func (s *signaling) queueCandidate(msg signalMessage) {
	if msg.Type != "new-ice-candidate" {
		return
	}
	var cand webrtc.ICECandidateInit
	if err := json.Unmarshal(msg.Data, &cand); err != nil || cand.Candidate == "" {
		return
	}
	select {
	case s.candidates <- cand:
	case <-s.ctx.Done():
	}
}

// pump keeps reading after the answer so trickled candidates keep flowing,
// and closes the candidates channel when the socket ends.
func (s *signaling) pump() {
	defer close(s.candidates)
	for {
		msg, err := s.readMessage(s.ctx)
		if err != nil {
			return
		}
		s.queueCandidate(msg)
	}
}

// attach feeds the device's trickled candidates into pc and ties the socket's
// lifetime to the peer connection.
func (s *signaling) attach(pc *webrtc.PeerConnection) {
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateClosed || state == webrtc.PeerConnectionStateFailed {
			s.Close()
		}
	})
	go func() {
		for cand := range s.candidates {
			_ = pc.AddICECandidate(cand)
		}
	}()
}

// ExchangeSDP sends a WebRTC offer to the device and returns its answer.
//
// The returned signaling is non-nil when the device answered over the
// signaling WebSocket; the caller must keep it open for the life of the
// session and call attach on it once the remote description is set. It is nil
// when the legacy HTTP endpoint was used, which needs no follow-up.
func (c *Client) ExchangeSDP(ctx context.Context, offer *webrtc.SessionDescription) (*webrtc.SessionDescription, *signaling, error) {
	sig, err := c.dialSignaling(ctx)
	if errors.Is(err, errSignalingUnsupported) {
		answer, lerr := c.exchangeSDPLegacy(ctx, offer)
		return answer, nil, lerr
	}
	if err != nil {
		return nil, nil, err
	}
	answer, err := sig.exchange(ctx, offer)
	if err != nil {
		sig.Close()
		return nil, nil, err
	}
	go sig.pump()
	return answer, sig, nil
}

// exchangeSDPLegacy is the one-shot POST /webrtc/session exchange that
// firmware before 0.5.9 offers. The device uses base64(JSON({type,sdp})) as
// the wire format.
func (c *Client) exchangeSDPLegacy(ctx context.Context, offer *webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	body, _ := json.Marshal(map[string]string{"sd": encodeSDP(offer)})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+legacySessionPath, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("SDP exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SDP exchange failed: HTTP %d", resp.StatusCode)
	}

	var wire struct {
		SD string `json:"sd"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil {
		return nil, fmt.Errorf("decoding SDP response: %w", err)
	}
	return decodeAnswer(wire.SD)
}
