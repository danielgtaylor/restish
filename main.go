package main

import (
	"github.com/danielgtaylor/restish/cli"
	"github.com/danielgtaylor/restish/oauth"
	"github.com/danielgtaylor/restish/openapi"
)

func main() {
	cli.Init("restish")

	// Register content encodings
	cli.AddEncoding("gzip", &cli.GzipEncoding{})
	cli.AddEncoding("br", &cli.BrotliEncoding{})

	// Register content type marshallers
	cli.AddContentType("application/cbor", 0.9, &cli.CBOR{})
	cli.AddContentType("application/msgpack", 0.8, &cli.MsgPack{})
	cli.AddContentType("application/ion", 0.6, &cli.Ion{})
	cli.AddContentType("application/json", 0.5, &cli.JSON{})
	cli.AddContentType("application/yaml", 0.5, &cli.YAML{})
	cli.AddContentType("text/*", 0.2, &cli.Text{})

	// Add link relation parsers
	cli.AddLinkParser(&cli.LinkHeaderParser{})

	// Register format loaders to auto-discover API descriptions
	cli.AddLoader(openapi.New())

	// Register auth schemes
	cli.AddAuth("http-basic", &cli.BasicAuth{})
	cli.AddAuth("oauth-client-credentials", &oauth.ClientCredentialsHandler{})
	cli.AddAuth("oauth-authorization-code", &oauth.AuthorizationCodeHandler{})

	// Run the CLI, parsing arguments, making requests, and printing responses.
	cli.Run()
}
