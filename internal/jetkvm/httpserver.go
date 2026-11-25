package jetkvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"
)

const mcpPath = "/mcp"

// HTTPOptions configures serving MCP over HTTP (streamable HTTP transport).
type HTTPOptions struct {
	// Addr is the listen address, e.g. 127.0.0.1:8080.
	Addr string
	Auth AuthMode
	// BearerToken is the shared secret for AuthBearer.
	BearerToken string
	OAuth       OAuthOptions
}

// Validate rejects configurations that are incomplete or unsafe. The server
// controls machines (keystrokes, virtual media, power-adjacent USB settings),
// so unauthenticated access is only allowed from the local machine.
func (o HTTPOptions) Validate() error {
	if o.Addr == "" {
		return errors.New("HTTP listen address is required")
	}
	switch o.Auth {
	case AuthNone:
		if !isLoopbackAddr(o.Addr) {
			return fmt.Errorf("--auth none is only allowed on a loopback address (127.0.0.1, ::1 or localhost), not %q: use --auth bearer or --auth oauth2", o.Addr)
		}
	case AuthBearer:
		if len(o.BearerToken) < minBearerTokenLength {
			return fmt.Errorf("--auth bearer needs a token of at least %d characters in JETKVM_MCP_BEARER_TOKEN", minBearerTokenLength)
		}
	case AuthOAuth2:
		for name, value := range map[string]string{
			"--oauth-introspection-url": o.OAuth.IntrospectionURL,
			"--oauth-issuer":            o.OAuth.Issuer,
			"--oauth-resource":          o.OAuth.Resource,
		} {
			if value == "" {
				return fmt.Errorf("--auth oauth2 needs %s", name)
			}
		}
		if _, err := url.Parse(o.OAuth.Resource); err != nil {
			return fmt.Errorf("--oauth-resource: %w", err)
		}
	default:
		return fmt.Errorf("unknown --auth %q: want none, bearer or oauth2", o.Auth)
	}
	return nil
}

func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// handler builds the HTTP handler: the MCP endpoint behind the chosen
// authentication, plus the OAuth protected resource metadata when applicable.
func (o HTTPOptions) handler(s *server.MCPServer) (http.Handler, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	mcp := http.Handler(server.NewStreamableHTTPServer(s))
	mux := http.NewServeMux()

	switch o.Auth {
	case AuthBearer:
		mcp = requireToken(bearerVerifier(o.BearerToken), "", mcp)
	case AuthOAuth2:
		resource, _ := url.Parse(o.OAuth.Resource)
		metadataPath := "/.well-known/oauth-protected-resource" + strings.TrimSuffix(resource.Path, "/")
		metadataURL := resource.Scheme + "://" + resource.Host + metadataPath
		mux.HandleFunc(metadataPath, o.OAuth.serveMetadata)
		mcp = requireToken(o.OAuth.verifier(), metadataURL, mcp)
	}
	mux.Handle(mcpPath, mcp)
	return mux, nil
}

// serveMetadata serves the OAuth 2.0 protected resource metadata (RFC 9728),
// which tells clients which authorization server to obtain tokens from.
func (o OAuthOptions) serveMetadata(w http.ResponseWriter, _ *http.Request) {
	metadata := map[string]any{
		"resource":                 o.Resource,
		"authorization_servers":    []string{o.Issuer},
		"bearer_methods_supported": []string{"header"},
	}
	if len(o.Scopes) > 0 {
		metadata["scopes_supported"] = o.Scopes
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(metadata)
}

// serveHTTP serves MCP over HTTP until interrupted.
func serveHTTP(s *server.MCPServer, o HTTPOptions) error {
	handler, err := o.handler(s)
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: o.Addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
