package main

import (
	"io"
	"reflect"
	"testing"

	"github.com/conallob/mcp-jetkvm/internal/jetkvm"
)

func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

func TestParseOptions(t *testing.T) {
	t.Run("stdio by default", func(t *testing.T) {
		opts, err := parseOptions(nil, env(nil), io.Discard)
		if err != nil || opts.HTTP != nil {
			t.Fatalf("got %+v, %v; want stdio", opts, err)
		}
	})

	t.Run("http with the default auth", func(t *testing.T) {
		opts, err := parseOptions([]string{"--http", "127.0.0.1:8080"}, env(nil), io.Discard)
		if err != nil || opts.HTTP == nil || opts.HTTP.Addr != "127.0.0.1:8080" || opts.HTTP.Auth != jetkvm.AuthNone {
			t.Fatalf("got %+v, %v", opts.HTTP, err)
		}
	})

	t.Run("bearer token comes from the environment", func(t *testing.T) {
		opts, err := parseOptions([]string{"--http", ":8080", "--auth", "bearer"},
			env(map[string]string{"JETKVM_MCP_BEARER_TOKEN": "tok"}), io.Discard)
		if err != nil || opts.HTTP.Auth != jetkvm.AuthBearer || opts.HTTP.BearerToken != "tok" {
			t.Fatalf("got %+v, %v", opts.HTTP, err)
		}
	})

	t.Run("oauth2 flags and secret", func(t *testing.T) {
		opts, err := parseOptions([]string{
			"--http", ":8080", "--auth", "oauth2",
			"--oauth-introspection-url", "https://idp/introspect",
			"--oauth-issuer", "https://idp",
			"--oauth-resource", "https://kvm/mcp",
			"--oauth-client-id", "client",
			"--oauth-scope", "kvm:read kvm:write",
		}, env(map[string]string{"JETKVM_MCP_OAUTH_CLIENT_SECRET": "s3cret"}), io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		want := jetkvm.OAuthOptions{
			IntrospectionURL: "https://idp/introspect",
			Issuer:           "https://idp",
			Resource:         "https://kvm/mcp",
			ClientID:         "client",
			ClientSecret:     "s3cret",
			Scopes:           []string{"kvm:read", "kvm:write"},
		}
		if opts.HTTP.Auth != jetkvm.AuthOAuth2 || !reflect.DeepEqual(opts.HTTP.OAuth, want) {
			t.Fatalf("got %+v", opts.HTTP)
		}
	})

	t.Run("unknown flag", func(t *testing.T) {
		if _, err := parseOptions([]string{"--nope"}, env(nil), io.Discard); err == nil {
			t.Fatal("expected an error")
		}
	})
}
