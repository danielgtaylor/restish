package main

import (
	"github.com/danielgtaylor/restish/cli"
	"github.com/danielgtaylor/restish/oauth"
	"github.com/danielgtaylor/restish/openapi"
)

var version string = "dev"
var commit string
var date string

func main() {
	cli.Init("restish", version)

	// Register default encodings, content type handlers, and link parsers.
	cli.Defaults()

	// Register format loaders to auto-discover API descriptions
	cli.AddLoader(openapi.New())

	// Register auth schemes
	cli.AddAuth("http-basic", &cli.BasicAuth{})
	cli.AddAuth("oauth-client-credentials", &oauth.ClientCredentialsHandler{})
	cli.AddAuth("oauth-authorization-code", &oauth.AuthorizationCodeHandler{})

	// Run the CLI, parsing arguments, making requests, and printing responses.
	cli.Run()
}
