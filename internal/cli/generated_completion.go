package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rest-sh/restish/v2/internal/content"
	"github.com/rest-sh/restish/v2/internal/output"
	"github.com/rest-sh/restish/v2/internal/request"
	"github.com/rest-sh/restish/v2/internal/spec"
	"github.com/spf13/cobra"
)

const (
	generatedCompletionTimeout       = 2 * time.Second
	generatedCompletionMaxBodyBytes  = 1 << 20
	generatedCompletionMaxCandidates = 100
	generatedCompletionMaxValueBytes = 256
)

func (c *CLI) completeGeneratedParam(cmd *cobra.Command, args []string, toComplete, apiName string, parameter *paramInfo, required, optional []*paramInfo, operations map[string]spec.Operation) ([]string, cobra.ShellCompDirective) {
	ctx, cancel := context.WithTimeout(cmd.Context(), generatedCompletionTimeout)
	defer cancel()
	candidates, err := c.generatedParamCandidates(ctx, cmd, args, toComplete, apiName, parameter, required, optional, operations)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return candidates, cobra.ShellCompDirectiveNoFileComp
}

func (c *CLI) generatedParamCandidates(ctx context.Context, cmd *cobra.Command, args []string, toComplete, apiName string, parameter *paramInfo, required, optional []*paramInfo, operations map[string]spec.Operation) ([]string, error) {
	if parameter == nil || parameter.completion == nil {
		return nil, nil
	}
	provider, ok := operations[parameter.completion.OperationID]
	if !ok || provider.Method != http.MethodGet && provider.Method != http.MethodHead || provider.HasBody {
		return nil, nil
	}
	values, err := generatedCompletionSourceValues(cmd, args, parameter, required, optional)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, query, headers := provider.Path, []generatedQueryParam{}, []string{}
	providerParams := completionProviderParams(provider)
	bindings := make([]string, 0, len(parameter.completion.Bindings))
	for target := range parameter.completion.Bindings {
		bindings = append(bindings, target)
	}
	sort.Strings(bindings)
	for _, target := range bindings {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		source := parameter.completion.Bindings[target]
		bound, exists := values[completionRefKey(source)]
		providerParam := providerParams[completionRefKey(target)]
		if !exists || providerParam == nil {
			return nil, nil
		}
		if err := validateGeneratedParamValues(providerParam, bound, target); err != nil {
			return nil, err
		}
		path, query, headers, err = addGeneratedParam(path, query, headers, providerParam, bound)
		if err != nil {
			return nil, err
		}
	}
	if pathParamRe.MatchString(path) {
		return nil, nil
	}
	profileName := completionProfileName(cmd)
	rawURL, err := c.generatedOperationURL(apiName, profileName, path, provider.OperationServer, query)
	if err != nil {
		return nil, err
	}
	return c.fetchGeneratedCompletion(ctx, cmd, rawURL, apiName, profileName, toComplete, headers, provider, *parameter.completion)
}

func completionRefKey(value string) string {
	location, name, _ := strings.Cut(value, ".")
	return paramKey(location, name)
}

func generatedCompletionSourceValues(cmd *cobra.Command, args []string, completing *paramInfo, required, optional []*paramInfo) (map[string][]string, error) {
	values := map[string][]string{}
	for i, parameter := range required {
		if i >= len(args) {
			break
		}
		values[paramKey(parameter.in, parameter.name)] = []string{args[i]}
	}
	for _, parameter := range optional {
		if parameter == completing || parameter.parent != nil || !cmd.Flags().Changed(parameter.flagName) {
			continue
		}
		value, err := generatedFlagValues(cmd, parameter)
		if err != nil {
			return nil, err
		}
		values[paramKey(parameter.in, parameter.name)] = value
	}
	return values, nil
}

func completionProviderParams(operation spec.Operation) map[string]*paramInfo {
	parameters := map[string]*paramInfo{}
	for _, parameter := range operation.Parameters {
		if parameter.XCLI.Ignore {
			continue
		}
		parameters[paramKey(parameter.In, parameter.Name)] = &paramInfo{
			name: parameter.Name, in: parameter.In, required: parameter.Required,
			typ: parameter.Type, itemType: parameter.ItemType, style: parameter.Style,
			explode: parameter.Explode, allowReserved: parameter.AllowReserved,
			contentMediaType: parameter.ContentMediaType,
		}
	}
	return parameters
}

func (c *CLI) fetchGeneratedCompletion(ctx context.Context, cmd *cobra.Command, rawURL, apiName, profileName, toComplete string, headers []string, provider spec.Operation, completion spec.ParamCompletion) ([]string, error) {
	gf, err := parseGlobalFlags(cmd)
	if err != nil {
		return nil, err
	}
	cacheKey := strings.Join([]string{
		provider.Method, provider.ID, rawURL, strings.Join(headers, "\x00"),
		completion.ValuePath, completion.DescriptionPath, toComplete,
		c.generatedCompletionConfigKey(apiName, gf),
		generatedCompletionProviderKey(provider),
	}, "\x00")
	opts, err := c.httpOptsFromGlobalFlags(gf, true)
	if err != nil {
		return nil, err
	}
	opts.Timeout = generatedCompletionTimeout
	opts.Retry = 0
	opts.RetryUnsafe = false
	opts.NoCache = true
	opts.Logger = nil
	opts.OnBeforeRequest = nil
	opts.OnResponse = nil
	opts.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	completionContent := generatedCompletionContentRegistry()
	opts.AcceptHeader = completionContent.AcceptHeader()
	opts.AcceptEncodingHeader = completionContent.AcceptEncodingHeader()
	mediaTypes := provider.ResponseMediaTypes
	if len(mediaTypes) == 0 && provider.ResponseMediaType != "" {
		mediaTypes = []string{provider.ResponseMediaType}
	}
	if accept := completionContent.AcceptHeaderFor(mediaTypes); accept != "" {
		opts.AcceptHeader = accept
	}
	prepared, err := c.prepareRequest(ctx, provider.Method, rawURL, profileName, opts, nil, headers, provider.NoAuth, authHandlerOptions{NoBrowser: true, NonInteractive: true}, &operationAuthPolicy{
		OptionalAuth: provider.OptionalAuth, NoAuth: provider.NoAuth,
		CredentialAlternatives: provider.CredentialAlternatives, Override: gf.Auth,
	}, false, apiName)
	if err != nil {
		return nil, err
	}
	defer c.closePreparedTransport(prepared)
	cacheAllowed := !gf.NoCache && !prepared.authEnabled && prepared.opts.ClientCertPath == "" && prepared.opts.ClientKeyPath == ""
	if cacheAllowed {
		if candidates, ok := c.loadGeneratedCompletionCache(apiName, profileName, cacheKey); ok {
			return candidates, nil
		}
	}
	prepared.opts.OnUnauthorized = nil
	response, err := request.Do(ctx, provider.Method, prepared.rawURL, nil, prepared.opts)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_ = response.Body.Close()
		return nil, fmt.Errorf("completion provider returned HTTP %d", response.StatusCode)
	}
	normalized, err := output.Normalize(response, completionContent, generatedCompletionMaxBodyBytes)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	doc := normalizedResponseDoc(normalized)
	value, err := selectGeneratedCompletionValue(ctx, doc, completion.ValuePath)
	if err != nil {
		return nil, err
	}
	var descriptions any
	if completion.DescriptionPath != "" {
		descriptions, err = selectGeneratedCompletionValue(ctx, doc, completion.DescriptionPath)
		if err != nil {
			return nil, err
		}
		if descriptions == nil {
			return nil, fmt.Errorf("completion description path returned null")
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	candidates, err := generatedCompletionCandidatesContext(ctx, value, descriptions, toComplete)
	if err != nil {
		return nil, err
	}
	if cacheAllowed {
		c.storeGeneratedCompletionCache(apiName, profileName, cacheKey, candidates)
	}
	return candidates, nil
}

func generatedCompletionContentRegistry() *content.Registry {
	registry := content.Default()
	registry.AddContentType(&content.ContentType{
		Name: "completion-json", MIMETypes: []string{"application/json"}, Suffixes: []string{"+json"}, Quality: 0.9,
		Marshal: json.Marshal,
		Unmarshal: func(data []byte) (any, error) {
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.UseNumber()
			var value any
			if err := decoder.Decode(&value); err != nil {
				return nil, err
			}
			if err := decoder.Decode(&struct{}{}); err != io.EOF {
				return nil, fmt.Errorf("invalid trailing JSON data")
			}
			return value, nil
		},
	})
	return registry
}

func selectGeneratedCompletionValue(ctx context.Context, value any, path string) (any, error) {
	parts := strings.Split(path, ".")
	current := []any{value}
	for partIndex, part := range parts {
		next := make([]any, 0, len(current))
		for i, item := range current {
			if i%128 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			if list, ok := item.([]any); ok {
				for j, entry := range list {
					if j%128 == 0 {
						if err := ctx.Err(); err != nil {
							return nil, err
						}
					}
					if selected, found := completionField(entry, part, completionHeaderField(parts, partIndex)); found {
						next = append(next, selected)
					}
				}
				continue
			}
			if selected, found := completionField(item, part, completionHeaderField(parts, partIndex)); found {
				next = append(next, selected)
			}
		}
		if len(next) == 0 {
			return nil, fmt.Errorf("completion path %q was not found", path)
		}
		current = next
	}
	if len(current) == 1 {
		return current[0], nil
	}
	return current, nil
}

func completionHeaderField(parts []string, index int) bool {
	return index == 1 && (parts[0] == "headers" || parts[0] == "headers_all")
}

func completionField(value any, name string, caseInsensitive bool) (any, bool) {
	switch value := value.(type) {
	case map[string]any:
		selected, ok := value[name]
		if ok || !caseInsensitive {
			return selected, ok
		}
		for key, selected := range value {
			if strings.EqualFold(key, name) {
				return selected, true
			}
		}
	case map[string]string:
		selected, ok := value[name]
		if ok || !caseInsensitive {
			return selected, ok
		}
		for key, selected := range value {
			if strings.EqualFold(key, name) {
				return selected, true
			}
		}
	}
	return nil, false
}

func generatedCompletionCandidates(value, descriptions any, prefix string) ([]string, error) {
	return generatedCompletionCandidatesContext(context.Background(), value, descriptions, prefix)
}

func generatedCompletionCandidatesContext(ctx context.Context, value, descriptions any, prefix string) ([]string, error) {
	values, err := completionScalars(ctx, value)
	if err != nil {
		return nil, err
	}
	var labels []string
	if descriptions != nil {
		labels, err = completionScalars(ctx, descriptions)
		if err != nil || len(labels) != len(values) {
			return nil, fmt.Errorf("completion descriptions do not match values")
		}
	}
	seen := map[string]bool{}
	result := make([]string, 0, min(len(values), generatedCompletionMaxCandidates))
	for i, value := range values {
		if i%128 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if !safeCompletionValue(value) || !strings.HasPrefix(value, prefix) || seen[value] {
			continue
		}
		seen[value] = true
		candidate := value
		if len(labels) > 0 {
			candidate = string(cobra.CompletionWithDesc(value, sanitizeCompletionDescription(labels[i])))
		}
		result = append(result, candidate)
		if len(result) == generatedCompletionMaxCandidates {
			break
		}
	}
	return result, nil
}

func completionScalars(ctx context.Context, value any) ([]string, error) {
	item := reflect.ValueOf(value)
	for item.IsValid() && (item.Kind() == reflect.Interface || item.Kind() == reflect.Pointer) {
		if item.IsNil() {
			return nil, fmt.Errorf("completion value is null")
		}
		item = item.Elem()
	}
	if !item.IsValid() {
		return nil, fmt.Errorf("completion value is null")
	}
	if item.Kind() != reflect.Array && item.Kind() != reflect.Slice {
		value, err := completionScalar(item)
		return []string{value}, err
	}
	values := make([]string, 0, min(item.Len(), generatedCompletionMaxCandidates))
	for i := 0; i < item.Len(); i++ {
		if i%128 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		value, err := completionScalar(item.Index(i))
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func completionScalar(value reflect.Value) (string, error) {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return "", fmt.Errorf("completion value is null")
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return fmt.Sprint(value.Interface()), nil
	default:
		return "", fmt.Errorf("completion value must be a scalar")
	}
}

func safeCompletionValue(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || strings.HasPrefix(value, "_activeHelp_ ") || len(value) > generatedCompletionMaxValueBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func sanitizeCompletionDescription(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	for len(value) > generatedCompletionMaxValueBytes {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	return value
}
