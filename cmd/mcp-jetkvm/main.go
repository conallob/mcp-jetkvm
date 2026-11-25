package main

import (
	"flag"
	"log"
	"os"
	"strings"

	"github.com/conallob/mcp-jetkvm/internal/jetkvm"
)

func main() {
	var (
		addr             = flag.String("http", "", "serve MCP over HTTP on this address (e.g. 127.0.0.1:8080) at /mcp, instead of stdio")
		auth             = flag.String("auth", "none", "HTTP authentication: none (loopback only), bearer or oauth2")
		introspectionURL = flag.String("oauth-introspection-url", "", "oauth2: the authorization server's token introspection endpoint")
		issuer           = flag.String("oauth-issuer", "", "oauth2: authorization server URL advertised to clients")
		resource         = flag.String("oauth-resource", "", "oauth2: this server's public MCP URL, e.g. https://host/mcp")
		clientID         = flag.String("oauth-client-id", "", "oauth2: client ID for the introspection endpoint")
		scopes           = flag.String("oauth-scope", "", "oauth2: space-separated scopes every token must have")
	)
	flag.Usage = func() {
		flag.PrintDefaults()
		log.Print("secrets are read from the environment: JETKVM_MCP_BEARER_TOKEN (--auth bearer), JETKVM_MCP_OAUTH_CLIENT_SECRET (--auth oauth2)")
	}
	flag.Parse()

	var opts jetkvm.Options
	if *addr != "" {
		opts.HTTP = &jetkvm.HTTPOptions{
			Addr:        *addr,
			Auth:        jetkvm.AuthMode(*auth),
			BearerToken: os.Getenv("JETKVM_MCP_BEARER_TOKEN"),
			OAuth: jetkvm.OAuthOptions{
				IntrospectionURL: *introspectionURL,
				Issuer:           *issuer,
				Resource:         *resource,
				ClientID:         *clientID,
				ClientSecret:     os.Getenv("JETKVM_MCP_OAUTH_CLIENT_SECRET"),
				Scopes:           strings.Fields(*scopes),
			},
		}
	}
	if err := jetkvm.RunServer(opts); err != nil {
		log.Fatal(err)
	}
}
