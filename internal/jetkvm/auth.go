package jetkvm

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
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

// verifier reports whether a bearer token is acceptable. A non-nil error means
// the check itself could not be made, which is not the same as a bad token.
type verifier func(ctx context.Context, token string) (bool, error)

func bearerVerifier(want string) verifier {
	return func(_ context.Context, token string) (bool, error) {
		return subtle.ConstantTimeCompare([]byte(token), []byte(want)) == 1, nil
	}
}

func (o OAuthOptions) verifier() verifier {
	client := &http.Client{Timeout: 5 * time.Second}
	return func(ctx context.Context, token string) (bool, error) {
		form := url.Values{"token": {token}, "token_type_hint": {"access_token"}}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.IntrospectionURL, strings.NewReader(form.Encode()))
		if err != nil {
			return false, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		if o.ClientID != "" {
			req.SetBasicAuth(url.QueryEscape(o.ClientID), url.QueryEscape(o.ClientSecret))
		}
		resp, err := client.Do(req)
		if err != nil {
			return false, fmt.Errorf("token introspection: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Errorf("token introspection: HTTP %d", resp.StatusCode)
		}

		var result struct {
			Active bool            `json:"active"`
			Scope  string          `json:"scope"`
			Aud    json.RawMessage `json:"aud"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return false, fmt.Errorf("token introspection: decoding response: %w", err)
		}
		if !result.Active {
			return false, nil
		}
		granted := strings.Fields(result.Scope)
		for _, want := range o.Scopes {
			if !contains(granted, want) {
				return false, nil
			}
		}
		if aud := audiences(result.Aud); len(aud) > 0 && !contains(aud, o.Resource) {
			return false, nil
		}
		return true, nil
	}
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

// requireToken rejects requests without an acceptable bearer token. metadataURL,
// if set, is advertised in the challenge so OAuth clients can discover the
// authorization server.
func requireToken(verify verifier, metadataURL string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if found && token != "" {
			ok, err := verify(r.Context(), token)
			if err != nil {
				http.Error(w, "cannot verify token", http.StatusServiceUnavailable)
				return
			}
			if ok {
				next.ServeHTTP(w, r)
				return
			}
		}
		challenge := "Bearer"
		if metadataURL != "" {
			challenge += fmt.Sprintf(" resource_metadata=%q", metadataURL)
		}
		w.Header().Set("WWW-Authenticate", challenge)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}
