package jetkvm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"

	"github.com/pion/webrtc/v3"
)

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
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("login failed: HTTP %d", resp.StatusCode)
	}
	return nil
}

type sdpEnvelope struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

// ExchangeSDP sends a WebRTC offer to /webrtc/session and returns the answer.
// The device uses base64(JSON({type,sdp})) as the wire format.
func (c *Client) ExchangeSDP(ctx context.Context, offer *webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	raw, err := json.Marshal(sdpEnvelope{Type: offer.Type.String(), SDP: offer.SDP})
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]string{"sd": base64.StdEncoding.EncodeToString(raw)})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/webrtc/session", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("SDP exchange: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SDP exchange failed: HTTP %d", resp.StatusCode)
	}

	var wire struct {
		SD string `json:"sd"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil {
		return nil, fmt.Errorf("decoding SDP response: %w", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(wire.SD)
	if err != nil {
		return nil, fmt.Errorf("base64 SDP answer: %w", err)
	}
	var answer sdpEnvelope
	if err := json.Unmarshal(decoded, &answer); err != nil {
		return nil, fmt.Errorf("parsing SDP answer: %w", err)
	}

	return &webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  answer.SDP,
	}, nil
}
