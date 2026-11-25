package jetkvm

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// AuthMode selects how HTTP requests are authenticated.
type AuthMode string

const (
	// AuthNone accepts every request. Only allowed on a loopback address.
	AuthNone AuthMode = "none"
	// AuthBearer accepts requests carrying one shared bearer token.
	AuthBearer AuthMode = "bearer"
	// AuthOAuth2 accepts OAuth 2.0 access tokens issued by an external
	// authorization server, checked by token introspection (RFC 7662).
	AuthOAuth2 AuthMode = "oauth2"
)

// minBearerTokenLength rejects tokens too short to resist guessing.
const minBearerTokenLength = 16

// OAuthOptions configures AuthOAuth2. The server acts as an OAuth 2.0 resource
// server: it never issues tokens, and tells clients which authorization server
// does through its protected resource metadata (RFC 9728).
type OAuthOptions struct {
	// IntrospectionURL is the authorization server's token introspection endpoint.
	IntrospectionURL string
	// Issuer is the authorization server URL advertised to clients.
	Issuer string
	// Resource is this server's public MCP URL, e.g. https://host/mcp. Tokens
	// that name an audience must name this.
	Resource string
	// ClientID and ClientSecret authenticate this server to the introspection endpoint.
	ClientID     string
	ClientSecret string
	// Scopes are all required of every token.
	Scopes []string
}

// errCannotVerify is what clients see when the introspection endpoint could not
// be reached. The cause is logged, not sent: it would reveal internal URLs.
var errCannotVerify = errors.New("cannot verify token")

func bearerVerifier(want string) auth.TokenVerifier {
	return func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		if subtle.ConstantTimeCompare([]byte(token), []byte(want)) != 1 {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{}, nil
	}
}

// verifier checks tokens by introspection. Scope and expiry are enforced by
// auth.RequireBearerToken from the TokenInfo returned here; the audience, which
// the SDK does not know about, is checked here.
func (o OAuthOptions) verifier() auth.TokenVerifier {
	client := &http.Client{Timeout: 5 * time.Second}
	return func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		result, err := o.introspect(ctx, client, token)
		if err != nil {
			slog.Warn("token introspection failed", "error", err)
			return nil, errCannotVerify
		}
		if !result.Active {
			return nil, auth.ErrInvalidToken
		}
		if aud := audiences(result.Aud); len(aud) > 0 && !contains(aud, o.Resource) {
			return nil, auth.ErrInvalidToken
		}
		info := &auth.TokenInfo{Scopes: strings.Fields(result.Scope)}
		if result.Exp > 0 {
			info.Expiration = time.Unix(result.Exp, 0)
		}
		return info, nil
	}
}

type introspection struct {
	Active bool            `json:"active"`
	Scope  string          `json:"scope"`
	Aud    json.RawMessage `json:"aud"`
	Exp    int64           `json:"exp"`
}

// introspect asks the authorization server about token (RFC 7662).
func (o OAuthOptions) introspect(ctx context.Context, client *http.Client, token string) (introspection, error) {
	var result introspection
	form := url.Values{"token": {token}, "token_type_hint": {"access_token"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.IntrospectionURL, strings.NewReader(form.Encode()))
	if err != nil {
		return result, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if o.ClientID != "" {
		req.SetBasicAuth(url.QueryEscape(o.ClientID), url.QueryEscape(o.ClientSecret))
	}
	resp, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return result, fmt.Errorf("decoding response: %w", err)
	}
	return result, nil
}

// audiences parses the "aud" claim, which is either a string or a list.
func audiences(raw json.RawMessage) []string {
	var one string
	if json.Unmarshal(raw, &one) == nil && one != "" {
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal(raw, &many)
	return many
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
