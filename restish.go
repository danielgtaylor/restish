// Package restish is the public Go API for embedding Restish in custom CLIs.
//
// The stock restish binary is built from cmd/restish. Embedders can construct a
// CLI directly, register organization-specific auth handlers, content types,
// encodings, link parsers, OpenAPI loaders, and formatters, then call Run with
// their own argv.
package restish

import (
	"github.com/rest-sh/restish/v2/auth"
	"github.com/rest-sh/restish/v2/config"
	internal_auth "github.com/rest-sh/restish/v2/internal/auth"
	internalcli "github.com/rest-sh/restish/v2/internal/cli"
	"github.com/rest-sh/restish/v2/internal/content"
	"github.com/rest-sh/restish/v2/internal/hypermedia"
	"github.com/rest-sh/restish/v2/internal/output"
	"github.com/rest-sh/restish/v2/internal/spec"
)

// DPoPCredentialDescription describes the authorization server and Resource
// binding used by Restish's native DPoP credential lifecycle.
type DPoPCredentialDescription = internal_auth.DPoPCredentialDescription

// DPoPIssuedCredential is a short-lived DPoP access token returned to Restish.
type DPoPIssuedCredential = internal_auth.DPoPIssuedCredential

// DPoPNonceChallenge requests one nonce-bound retry at the credential endpoint.
type DPoPNonceChallenge = internal_auth.DPoPNonceChallenge

// DPoPCredentialSource supplies provider-issued target credentials while
// Restish retains custody of target keys, proofs, tokens, and cache state.
type DPoPCredentialSource = internal_auth.DPoPCredentialSource

// CLI is an embeddable Restish runtime.
//
// The zero value is not intended for use; call New so standard content types,
// loaders, formatters, paths, and I/O streams are initialized.
type CLI = internalcli.CLI

// Config is the Restish configuration loaded by CLI.Run.
type Config = config.Config

// APIConfig holds one registered API's base URL, spec source, profiles, and
// generated-command options.
type APIConfig = config.APIConfig

// ProfileConfig holds one API profile's headers, query parameters, auth, and
// TLS settings.
type ProfileConfig = config.ProfileConfig

// AuthConfig holds one configured auth handler type and its parameters.
type AuthConfig = config.AuthConfig

// ContentType describes one request/response body encoder and decoder.
type ContentType = content.ContentType

// Encoding describes one HTTP content-encoding codec.
type Encoding = content.Encoding

// Formatter renders normalized responses for -o/--rsh-output-format.
type Formatter = output.Formatter

// Response is the normalized HTTP response passed to custom formatters and
// returned by FetchResponse.
type Response = output.Response

// LinkParser extracts hypermedia links from response headers or bodies.
type LinkParser = hypermedia.Parser

// Link is a normalized hypermedia link returned by custom link parsers.
type Link = hypermedia.Link

// Loader detects and loads OpenAPI-like API descriptions.
type Loader = spec.Loader

// LoadOptions carries source metadata for custom spec loaders.
type LoadOptions = spec.LoadOptions

// APISpec is the parsed API description returned by custom spec loaders.
type APISpec = spec.APISpec

// AuthHandler is implemented by custom authentication schemes.
type AuthHandler = auth.Handler

// AuthParam describes a custom auth handler parameter.
type AuthParam = auth.Param

// AuthContext is passed to custom auth handlers during request authentication.
type AuthContext = auth.AuthContext

// CommandSurface controls the command tree exposed by embedded custom CLIs.
type CommandSurface = internalcli.CommandSurface

// New returns a CLI wired to the real OS stdin/stdout/stderr and the default
// Restish registries. Customize it with SetCommandName,
// SetCommandDescription, SetVersion, SetSignalHandling, SetDefaultConfig,
// SetCommandSurface, AddAuthHandler, AddContentType, AddEncoding,
// AddLinkParser, AddLoader, and AddFormatter before calling Run.
func New() *CLI {
	return internalcli.New()
}

// NewDPoPAuthHandler creates Restish's native operation-aware DPoP handler for
// an in-process provider credential source.
func NewDPoPAuthHandler(source DPoPCredentialSource) AuthHandler {
	return &internal_auth.DPoP{Source: source}
}

// Version is the current build version. Set github.com/rest-sh/restish/v2/internal/cli.Version
// from a custom main package when branding or release metadata differs.
func Version() string {
	return internalcli.Version
}
