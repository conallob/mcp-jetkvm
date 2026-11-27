package jetkvm

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

const testToken = "0123456789abcdef-test-token"

func TestHTTPOptionsValidate(t *testing.T) {
	oauth := OAuthOptions{IntrospectionURL: "https://idp/introspect", Issuer: "https://idp", Resource: "https://kvm.example/mcp"}
	tests := []struct {
		name    string
		opts    HTTPOptions
		wantErr bool
	}{
		{"none on 127.0.0.1", HTTPOptions{Addr: "127.0.0.1:8080", Auth: AuthNone}, false},
		{"none on localhost", HTTPOptions{Addr: "localhost:8080", Auth: AuthNone}, false},
		{"none on ::1", HTTPOptions{Addr: "[::1]:8080", Auth: AuthNone}, false},
		{"none on all interfaces", HTTPOptions{Addr: ":8080", Auth: AuthNone}, true},
		{"none on 0.0.0.0", HTTPOptions{Addr: "0.0.0.0:8080", Auth: AuthNone}, true},
		{"none on a LAN address", HTTPOptions{Addr: "192.168.1.5:8080", Auth: AuthNone}, true},
		{"bearer on all interfaces", HTTPOptions{Addr: ":8080", Auth: AuthBearer, BearerToken: testToken}, false},
		{"bearer with a short token", HTTPOptions{Addr: ":8080", Auth: AuthBearer, BearerToken: "short"}, true},
		{"bearer without a token", HTTPOptions{Addr: ":8080", Auth: AuthBearer}, true},
		{"oauth2 complete", HTTPOptions{Addr: ":8080", Auth: AuthOAuth2, OAuth: oauth}, false},
		{"oauth2 without issuer", HTTPOptions{Addr: ":8080", Auth: AuthOAuth2, OAuth: OAuthOptions{IntrospectionURL: "x", Resource: "y"}}, true},
		{"unknown mode", HTTPOptions{Addr: "127.0.0.1:8080", Auth: "basic"}, true},
		{"no address", HTTPOptions{Auth: AuthNone}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.opts.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// request posts an MCP initialize to the handler, optionally with an
// Authorization header, and returns the recorded response.
func request(h http.Handler, authorization string) *httptest.ResponseRecorder {
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`
	req := httptest.NewRequest(http.MethodPost, mcpPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestBearerAuth(t *testing.T) {
	h, err := HTTPOptions{Addr: ":8080", Auth: AuthBearer, BearerToken: testToken}.handler(newMCPServer("test"))
	if err != nil {
		t.Fatal(err)
	}

	for name, header := range map[string]string{
		"no header":    "",
		"wrong token":  "Bearer nope",
		"wrong scheme": "Basic " + testToken,
		"empty bearer": "Bearer ",
		"no scheme":    testToken,
	} {
		t.Run(name, func(t *testing.T) {
			if rec := request(h, header); rec.Code != http.StatusUnauthorized {
				t.Fatalf("code = %d, want 401", rec.Code)
			}
		})
	}

	if rec := request(h, "Bearer "+testToken); rec.Code != http.StatusOK {
		t.Fatalf("valid token: code = %d, want 200: %s", rec.Code, rec.Body)
	}
}

// introspectionServer answers RFC 7662 requests with the given response body,
// after checking the client credentials and form fields.
func introspectionServer(t *testing.T, response string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, secret, _ := r.BasicAuth()
		if id != "client" || secret != "s3cret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = r.ParseForm()
		if r.PostForm.Get("token") != "good-token" {
			_, _ = w.Write([]byte(`{"active":false}`))
			return
		}
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOAuthVerifier(t *testing.T) {
	const resource = "https://kvm.example/mcp"
	exp := time.Now().Add(time.Hour).Unix()
	tests := []struct {
		name     string
		response string
		token    string
		invalid  bool
	}{
		{"active", `{"active":true}`, "good-token", false},
		{"unknown token", `{"active":true}`, "other", true},
		{"inactive", `{"active":false}`, "good-token", true},
		{"audience matches", `{"active":true,"aud":"` + resource + `"}`, "good-token", false},
		{"audience in list", `{"active":true,"aud":["other","` + resource + `"]}`, "good-token", false},
		{"audience mismatch", `{"active":true,"aud":"https://other.example/mcp"}`, "good-token", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idp := introspectionServer(t, tt.response)
			o := OAuthOptions{IntrospectionURL: idp.URL, Resource: resource, ClientID: "client", ClientSecret: "s3cret"}
			info, err := o.verifier()(t.Context(), tt.token, nil)
			if tt.invalid {
				if !errors.Is(err, auth.ErrInvalidToken) {
					t.Fatalf("err = %v, want ErrInvalidToken", err)
				}
				return
			}
			if err != nil || info == nil {
				t.Fatalf("verify = %v, %v", info, err)
			}
		})
	}

	t.Run("scopes and expiry come from the introspection response", func(t *testing.T) {
		idp := introspectionServer(t, `{"active":true,"scope":"kvm:read kvm:write","exp":`+jsonNumber(exp)+`}`)
		o := OAuthOptions{IntrospectionURL: idp.URL, ClientID: "client", ClientSecret: "s3cret"}
		info, err := o.verifier()(t.Context(), "good-token", nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(info.Scopes) != 2 || info.Scopes[1] != "kvm:write" || info.Expiration.Unix() != exp {
			t.Fatalf("unexpected token info: %+v", info)
		}
	})

	t.Run("wrong client credentials is not reported as a bad token", func(t *testing.T) {
		idp := introspectionServer(t, `{"active":true}`)
		o := OAuthOptions{IntrospectionURL: idp.URL, ClientID: "client", ClientSecret: "wrong"}
		if _, err := o.verifier()(t.Context(), "good-token", nil); !errors.Is(err, errCannotVerify) {
			t.Fatalf("err = %v, want errCannotVerify", err)
		}
	})
}

func jsonNumber(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func oauthHandler(t *testing.T, idpURL string, scopes ...string) http.Handler {
	t.Helper()
	h, err := HTTPOptions{Addr: ":8080", Auth: AuthOAuth2, OAuth: OAuthOptions{
		IntrospectionURL: idpURL,
		Issuer:           "https://idp.example",
		Resource:         "https://kvm.example/mcp",
		ClientID:         "client",
		ClientSecret:     "s3cret",
		Scopes:           scopes,
	}}.handler(newMCPServer("test"))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestOAuthHandler(t *testing.T) {
	idp := introspectionServer(t, `{"active":true,"scope":"kvm"}`)

	t.Run("metadata is public", func(t *testing.T) {
		h := oauthHandler(t, idp.URL, "kvm")
		req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource/mcp", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("code = %d", rec.Code)
		}
		var md struct {
			Resource   string   `json:"resource"`
			AuthServer []string `json:"authorization_servers"`
			Scopes     []string `json:"scopes_supported"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &md); err != nil {
			t.Fatal(err)
		}
		if md.Resource != "https://kvm.example/mcp" || len(md.AuthServer) != 1 || md.AuthServer[0] != "https://idp.example" || len(md.Scopes) != 1 {
			t.Fatalf("unexpected metadata: %+v", md)
		}
	})

	t.Run("challenge points at the metadata", func(t *testing.T) {
		rec := request(oauthHandler(t, idp.URL, "kvm"), "")
		challenge := rec.Header().Get("WWW-Authenticate")
		if rec.Code != http.StatusUnauthorized ||
			!strings.Contains(challenge, `resource_metadata="https://kvm.example/.well-known/oauth-protected-resource/mcp"`) {
			t.Fatalf("code=%d WWW-Authenticate=%q", rec.Code, challenge)
		}
	})

	t.Run("a valid token with the required scope is accepted", func(t *testing.T) {
		if rec := request(oauthHandler(t, idp.URL, "kvm"), "Bearer good-token"); rec.Code != http.StatusOK {
			t.Fatalf("code = %d: %s", rec.Code, rec.Body)
		}
	})

	t.Run("a missing scope is forbidden", func(t *testing.T) {
		if rec := request(oauthHandler(t, idp.URL, "kvm", "admin"), "Bearer good-token"); rec.Code != http.StatusForbidden {
			t.Fatalf("code = %d, want 403", rec.Code)
		}
	})

	t.Run("an unknown token is unauthorized", func(t *testing.T) {
		if rec := request(oauthHandler(t, idp.URL), "Bearer other"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("code = %d, want 401", rec.Code)
		}
	})

	t.Run("an unreachable introspection endpoint fails closed without leaking its URL", func(t *testing.T) {
		down := httptest.NewServer(http.NotFoundHandler())
		url := down.URL
		down.Close()
		rec := request(oauthHandler(t, url), "Bearer good-token")
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("code = %d, want 500", rec.Code)
		}
		if strings.Contains(rec.Body.String(), url) {
			t.Fatalf("response leaks the introspection URL: %q", rec.Body)
		}
	})
}

// TestMCPOverHTTP runs a real MCP initialize and tools/call through the
// authenticated handler.
func TestMCPOverHTTP(t *testing.T) {
	t.Setenv("JETKVM_DEVICES", "")
	t.Setenv("JETKVM_HOST", "kvm.example")
	h, err := HTTPOptions{Addr: ":8080", Auth: AuthBearer, BearerToken: testToken}.handler(newMCPServer("test"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	post := func(token, body string) (*http.Response, string) {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+mcpPath, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		return resp, sb.String()
	}

	const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`
	if resp, _ := post("", initialize); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated initialize: %d, want 401", resp.StatusCode)
	}
	resp, body := post(testToken, initialize)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "mcp-jetkvm") {
		t.Fatalf("initialize: %d %q", resp.StatusCode, body)
	}

	call := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_devices","arguments":{}}}`
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+mcpPath, strings.NewReader(call))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+testToken)
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	callResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer callResp.Body.Close()
	if callResp.StatusCode != http.StatusOK {
		t.Fatalf("tools/call: %d", callResp.StatusCode)
	}
}
