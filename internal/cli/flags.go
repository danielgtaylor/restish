package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rest-sh/restish/v2/internal/request"
)

// GlobalFlags holds the parsed value of every persistent rsh-* flag plus the
// corresponding RSH_* environment-variable override. It is populated once in
// PersistentPreRunE and stored on the command context; command RunE functions
// retrieve it via globalFlagsFromContext.
type GlobalFlags struct {
	Headers          []string
	Query            []string
	PromptSecrets    []string
	Server           string
	OutputFormat     string
	OutputFormatSet  bool
	Print            string
	PrintSet         bool
	Silent           bool
	Columns          string
	SortBy           string
	ContentType      string
	Filter           string
	FilterLang       string
	HeadersShorthand bool // --rsh-headers
	StatusShorthand  bool // --rsh-status
	Verbose          int
	Insecure         bool
	ClientCert       string
	ClientKey        string
	TLSSigner        string
	TLSSignerParams  []string
	CACert           string
	TLSMinVersion    string
	IgnoreStatus     bool
	Timeout          string
	Profile          string
	Auth             string
	NoCache          bool
	NoBrowser        bool
	Retry            int // -1 means "not set by user"
	RetryUnsafe      bool
	RetryMaxWait     string
	RetryMaxWaitSet  bool
	NoPaginate       bool
	Collect          bool
	MaxPages         int
	MaxItems         int
	MaxBodySize      int
}

type globalFlagsContextKey struct{}

// parseGlobalFlags reads persistent flags from cmd (which includes all
// inherited persistent flags from parent commands) and merges RSH_* env vars.
// Env vars have lower precedence than explicit flag values.
func parseGlobalFlags(cmd *cobra.Command) (GlobalFlags, error) {
	var gf GlobalFlags

	// StringArray flags
	gf.Headers, _ = cmd.Flags().GetStringArray("rsh-header")
	gf.Query, _ = cmd.Flags().GetStringArray("rsh-query")
	gf.PromptSecrets, _ = cmd.Flags().GetStringArray("rsh-prompt-secret")
	gf.TLSSignerParams, _ = cmd.Flags().GetStringArray("rsh-tls-signer-param")

	// String flags
	gf.Server, _ = cmd.Flags().GetString("rsh-server")
	gf.OutputFormat, _ = cmd.Flags().GetString("rsh-output-format")
	gf.OutputFormatSet = cmd.Flags().Changed("rsh-output-format")
	gf.Print, _ = cmd.Flags().GetString("rsh-print")
	gf.PrintSet = cmd.Flags().Changed("rsh-print")
	gf.Columns, _ = cmd.Flags().GetString("rsh-columns")
	gf.SortBy, _ = cmd.Flags().GetString("rsh-sort-by")
	gf.ContentType, _ = cmd.Flags().GetString("rsh-content-type")
	gf.Filter, _ = cmd.Flags().GetString("rsh-filter")
	gf.FilterLang, _ = cmd.Flags().GetString("rsh-filter-lang")
	gf.ClientCert, _ = cmd.Flags().GetString("rsh-client-cert")
	gf.ClientKey, _ = cmd.Flags().GetString("rsh-client-key")
	gf.TLSSigner, _ = cmd.Flags().GetString("rsh-tls-signer")
	gf.CACert, _ = cmd.Flags().GetString("rsh-ca-cert")
	gf.TLSMinVersion, _ = cmd.Flags().GetString("rsh-tls-min-version")
	gf.Profile, _ = cmd.Flags().GetString("rsh-profile")
	gf.Auth, _ = cmd.Flags().GetString("rsh-auth")
	gf.Timeout, _ = cmd.Flags().GetString("rsh-timeout")

	// Bool flags
	gf.Silent, _ = cmd.Flags().GetBool("rsh-silent")
	gf.HeadersShorthand, _ = cmd.Flags().GetBool("rsh-headers")
	gf.StatusShorthand, _ = cmd.Flags().GetBool("rsh-status")
	gf.Insecure, _ = cmd.Flags().GetBool("rsh-insecure")
	gf.IgnoreStatus, _ = cmd.Flags().GetBool("rsh-ignore-status-code")
	gf.NoCache, _ = cmd.Flags().GetBool("rsh-no-cache")
	gf.NoBrowser, _ = cmd.Flags().GetBool("rsh-no-browser")
	gf.RetryUnsafe, _ = cmd.Flags().GetBool("rsh-retry-unsafe")
	gf.NoPaginate, _ = cmd.Flags().GetBool("rsh-no-paginate")
	gf.Collect, _ = cmd.Flags().GetBool("rsh-collect")

	// Count flag
	gf.Verbose, _ = cmd.Flags().GetCount("rsh-verbose")

	// Int flags
	gf.Retry, _ = cmd.Flags().GetInt("rsh-retry")
	gf.RetryMaxWait, _ = cmd.Flags().GetString("rsh-retry-max-wait")
	gf.RetryMaxWaitSet = cmd.Flags().Changed("rsh-retry-max-wait")
	gf.MaxPages, _ = cmd.Flags().GetInt("rsh-max-pages")
	gf.MaxItems, _ = cmd.Flags().GetInt("rsh-max-items")
	gf.MaxBodySize, _ = cmd.Flags().GetInt("rsh-max-body-size")

	// Apply env-var overrides (env var loses to explicit flag). Headers are
	// merged by name so CLI values replace matching env defaults instead of
	// becoming multi-value headers.
	if v := os.Getenv("RSH_HEADER"); v != "" {
		headers := splitEnvList(v)
		if err := validateEnvHeaders(headers); err != nil {
			return gf, err
		}
		if cmd.Flags().Changed("rsh-header") {
			var err error
			gf.Headers, err = mergeHeaderOptions(headers, gf.Headers)
			if err != nil {
				return gf, err
			}
		} else {
			gf.Headers = headers
		}
	}
	// For query StringArray flags, preserve the existing flag-replaces-env
	// semantics until query merge behavior is designed separately.
	if v := os.Getenv("RSH_QUERY"); v != "" && !cmd.Flags().Changed("rsh-query") {
		query := splitEnvList(v)
		if err := validateEnvQuery(query); err != nil {
			return gf, err
		}
		gf.Query = append(query, gf.Query...)
	}
	if v := os.Getenv("RSH_OUTPUT_FORMAT"); v != "" && !cmd.Flags().Changed("rsh-output-format") {
		gf.OutputFormat = v
		gf.OutputFormatSet = true
	}
	if v := os.Getenv("RSH_PRINT"); v != "" && !cmd.Flags().Changed("rsh-print") {
		gf.Print = v
		gf.PrintSet = true
	}

	if strings.EqualFold(strings.TrimSpace(gf.OutputFormat), "auto") {
		gf.OutputFormat = ""
		if !cmd.Flags().Changed("rsh-output-format") {
			gf.OutputFormatSet = false
		}
	}
	if strings.EqualFold(strings.TrimSpace(gf.Print), "auto") {
		gf.Print = ""
		gf.PrintSet = false
	}
	if v := os.Getenv("RSH_FILTER"); v != "" && !cmd.Flags().Changed("rsh-filter") {
		gf.Filter = v
	}
	if v := os.Getenv("RSH_INSECURE"); isTruthy(v) && !cmd.Flags().Changed("rsh-insecure") {
		gf.Insecure = true
	}
	if v := os.Getenv("RSH_NO_CACHE"); isTruthy(v) && !cmd.Flags().Changed("rsh-no-cache") {
		gf.NoCache = true
	}
	if v := os.Getenv("RSH_TIMEOUT"); v != "" && !cmd.Flags().Changed("rsh-timeout") {
		if err := validateTimeoutDuration("RSH_TIMEOUT", v); err != nil {
			return gf, err
		}
		gf.Timeout = v
	}
	if v := os.Getenv("RSH_RETRY"); v != "" && !cmd.Flags().Changed("rsh-retry") {
		n, err := strconv.Atoi(v)
		if err != nil {
			return gf, fmt.Errorf("invalid RSH_RETRY %q: %w", v, err)
		}
		if n < 0 {
			return gf, fmt.Errorf("invalid RSH_RETRY %q: must be greater than or equal to 0", v)
		}
		gf.Retry = n
	}
	if v := os.Getenv("RSH_RETRY_UNSAFE"); isTruthy(v) && !cmd.Flags().Changed("rsh-retry-unsafe") {
		gf.RetryUnsafe = true
	}
	if v := os.Getenv("RSH_RETRY_MAX_WAIT"); v != "" && !cmd.Flags().Changed("rsh-retry-max-wait") {
		gf.RetryMaxWait = v
		gf.RetryMaxWaitSet = true
	}
	if v := os.Getenv("RSH_PROFILE"); v != "" && !cmd.Flags().Changed("rsh-profile") {
		gf.Profile = v
	}
	if v := os.Getenv("RSH_AUTH"); v != "" && !cmd.Flags().Changed("rsh-auth") {
		gf.Auth = v
	}

	if err := validateNonNegativeGlobalFlags(cmd, gf); err != nil {
		return gf, err
	}
	if err := validateFilterLangFlag(cmd, gf); err != nil {
		return gf, err
	}
	if err := validateTLSMinVersionFlag(cmd, gf); err != nil {
		return gf, err
	}
	return gf, nil
}

func validateNonNegativeGlobalFlags(cmd *cobra.Command, gf GlobalFlags) error {
	if cmd.Flags().Changed("rsh-timeout") {
		if err := validateTimeoutDuration("--rsh-timeout", gf.Timeout); err != nil {
			return err
		}
	}
	if cmd.Flags().Changed("rsh-retry") && gf.Retry < 0 {
		return fmt.Errorf("invalid --rsh-retry %d: must be greater than or equal to 0", gf.Retry)
	}
	if gf.MaxPages < 0 {
		return fmt.Errorf("invalid --rsh-max-pages %d: must be greater than or equal to 0", gf.MaxPages)
	}
	if gf.MaxItems < 0 {
		return fmt.Errorf("invalid --rsh-max-items %d: must be greater than or equal to 0", gf.MaxItems)
	}
	if gf.MaxBodySize < 0 {
		return fmt.Errorf("invalid --rsh-max-body-size %d: must be greater than or equal to 0", gf.MaxBodySize)
	}
	return nil
}

func validateFilterLangFlag(cmd *cobra.Command, gf GlobalFlags) error {
	if !cmd.Flags().Changed("rsh-filter-lang") {
		return nil
	}
	value := strings.TrimSpace(gf.FilterLang)
	switch strings.ToLower(value) {
	case "shorthand", "jq":
		return nil
	default:
		return fmt.Errorf("invalid --rsh-filter-lang %q: must be one of shorthand, jq", gf.FilterLang)
	}
}

func validateTLSMinVersionFlag(cmd *cobra.Command, gf GlobalFlags) error {
	if !cmd.Flags().Changed("rsh-tls-min-version") {
		return nil
	}
	if _, err := request.TLSVersionFromString(gf.TLSMinVersion); err != nil {
		return fmt.Errorf("invalid --rsh-tls-min-version %q: must be one of TLS1.2, TLS1.3", gf.TLSMinVersion)
	}
	return nil
}

func validateTimeoutDuration(source, value string) error {
	d, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("invalid %s %q: %w", source, value, err)
	}
	if d < 0 {
		return fmt.Errorf("invalid %s %q: must be greater than or equal to 0", source, value)
	}
	return nil
}

func splitEnvList(v string) []string {
	var parts []string
	var b strings.Builder
	escaped := false
	for _, r := range v {
		if escaped {
			if r != ',' && r != '\\' {
				b.WriteRune('\\')
			}
			b.WriteRune(r)
			escaped = false
			continue
		}
		switch r {
		case '\\':
			escaped = true
		case ',':
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	if escaped {
		b.WriteRune('\\')
	}
	parts = append(parts, b.String())

	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func validateEnvHeaders(headers []string) error {
	for _, header := range headers {
		if _, _, err := request.ParseHeaderOption(header); err != nil {
			return fmt.Errorf("invalid RSH_HEADER entry: %w", err)
		}
	}
	return nil
}

func mergeHeaderOptions(base, overrides []string) ([]string, error) {
	merged := make([]string, 0, len(base))
	indexByName := map[string]int{}
	for _, header := range append(append([]string(nil), base...), overrides...) {
		name, _, err := request.ParseHeaderOption(header)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(strings.TrimSpace(name))
		if idx, ok := indexByName[key]; ok {
			merged[idx] = header
			continue
		}
		indexByName[key] = len(merged)
		merged = append(merged, header)
	}
	return merged, nil
}

func validateEnvQuery(query []string) error {
	for _, item := range query {
		if _, _, err := request.ParseQueryOption(item); err != nil {
			return fmt.Errorf("invalid RSH_QUERY entry: %w", err)
		}
	}
	return nil
}

// isTruthy reports whether a string env value means "true".
func isTruthy(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "1" || s == "true" || s == "yes"
}

// withGlobalFlags returns a context with gf stored under globalFlagsContextKey.
func withGlobalFlags(ctx context.Context, gf GlobalFlags) context.Context {
	return context.WithValue(ctx, globalFlagsContextKey{}, gf)
}

// globalFlagsFromContext retrieves the GlobalFlags stored on ctx.
// If none are present (e.g. in tests that bypass PersistentPreRunE), it
// falls back to empty GlobalFlags with Retry=-1 (the sentinel for "use default").
func globalFlagsFromContext(ctx context.Context) GlobalFlags {
	if gf, ok := ctx.Value(globalFlagsContextKey{}).(GlobalFlags); ok {
		return gf
	}
	return GlobalFlags{Retry: -1, MaxPages: 25}
}

func requestContext(cmd *cobra.Command) context.Context {
	if cmd != nil && cmd.Context() != nil {
		return cmd.Context()
	}
	return context.Background()
}
