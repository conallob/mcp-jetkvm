package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/conallob/mcp-jetkvm/internal/jetkvm"
)

func main() {
	opts, err := parseOptions(os.Args[1:], os.Getenv, os.Stderr)
	if err == flag.ErrHelp {
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	if err := jetkvm.RunServer(opts); err != nil {
		log.Fatal(err)
	}
}

// parseOptions reads the command line. Secrets come from the environment
// (through getenv) rather than flags, so they stay out of process listings.
func parseOptions(args []string, getenv func(string) string, output io.Writer) (jetkvm.Options, error) {
	fs := flag.NewFlagSet("mcp-jetkvm", flag.ContinueOnError)
	fs.SetOutput(output)
	var (
		addr             = fs.String("http", "", "serve MCP over HTTP on this address (e.g. 127.0.0.1:8080) at /mcp, instead of stdio")
		auth             = fs.String("auth", "none", "HTTP authentication: none (loopback only), bearer or oauth2")
		introspectionURL = fs.String("oauth-introspection-url", "", "oauth2: the authorization server's token introspection endpoint")
		issuer           = fs.String("oauth-issuer", "", "oauth2: authorization server URL advertised to clients")
		resource         = fs.String("oauth-resource", "", "oauth2: this server's public MCP URL, e.g. https://host/mcp")
		clientID         = fs.String("oauth-client-id", "", "oauth2: client ID for the introspection endpoint")
		scopes           = fs.String("oauth-scope", "", "oauth2: space-separated scopes every token must have")
	)
	fs.Usage = func() {
		fs.PrintDefaults()
		_, _ = fmt.Fprintln(output, "secrets are read from the environment: JETKVM_MCP_BEARER_TOKEN (--auth bearer), JETKVM_MCP_OAUTH_CLIENT_SECRET (--auth oauth2)")
	}
	if err := fs.Parse(args); err != nil {
		return jetkvm.Options{}, err
	}

	var opts jetkvm.Options
	if *addr != "" {
		opts.HTTP = &jetkvm.HTTPOptions{
			Addr:        *addr,
			Auth:        jetkvm.AuthMode(*auth),
			BearerToken: getenv("JETKVM_MCP_BEARER_TOKEN"),
			OAuth: jetkvm.OAuthOptions{
				IntrospectionURL: *introspectionURL,
				Issuer:           *issuer,
				Resource:         *resource,
				ClientID:         *clientID,
				ClientSecret:     getenv("JETKVM_MCP_OAUTH_CLIENT_SECRET"),
				Scopes:           strings.Fields(*scopes),
			},
		}
	}
	return opts, nil
}
