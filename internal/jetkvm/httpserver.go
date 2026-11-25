package jetkvm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
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
func (o HTTPOptions) handler(s *mcp.Server) (http.Handler, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	// The SDK rejects requests that reach a loopback listener with a foreign
	// Host header, to stop DNS rebinding. That also rejects a reverse proxy on
	// the same machine forwarding for its public name, so it is only kept when
	// there is no authentication to stop a rebinding attack.
	mcpHandler := http.Handler(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return s },
		&mcp.StreamableHTTPOptions{DisableLocalhostProtection: o.Auth != AuthNone},
	))
	mux := http.NewServeMux()

	switch o.Auth {
	case AuthBearer:
		// A shared token carries no expiry.
		mcpHandler = auth.RequireBearerToken(bearerVerifier(o.BearerToken),
			&auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(mcpHandler)
	case AuthOAuth2:
		resource, _ := url.Parse(o.OAuth.Resource)
		metadataPath := "/.well-known/oauth-protected-resource" + strings.TrimSuffix(resource.Path, "/")
		metadataURL := resource.Scheme + "://" + resource.Host + metadataPath
		mux.Handle(metadataPath, auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
			Resource:               o.OAuth.Resource,
			AuthorizationServers:   []string{o.OAuth.Issuer},
			BearerMethodsSupported: []string{"header"},
			ScopesSupported:        o.OAuth.Scopes,
		}))
		mcpHandler = auth.RequireBearerToken(o.OAuth.verifier(), &auth.RequireBearerTokenOptions{
			ResourceMetadataURL: metadataURL,
			Scopes:              o.OAuth.Scopes,
			// Not every provider returns "exp" from introspection; an inactive
			// token is already rejected by the verifier.
			AllowMissingExpiration: true,
		})(mcpHandler)
	}
	mux.Handle(mcpPath, mcpHandler)
	return mux, nil
}

// serveHTTP serves MCP over HTTP until interrupted.
func serveHTTP(s *mcp.Server, o HTTPOptions) error {
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
