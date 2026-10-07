package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/rest-sh/restish/v2/internal/filter"
	"github.com/rest-sh/restish/v2/internal/output"
	"github.com/rest-sh/restish/v2/internal/spec"
	pluginwire "github.com/rest-sh/restish/v2/plugin"
	"github.com/spf13/cobra"
)

func (c *CLI) handleCommandPluginMessage(cmd *cobra.Command, requestCtx context.Context, writer *commandPluginWriter, requestWG *sync.WaitGroup, progress *commandProgressRenderer, msgType string, raw []byte) (bool, error) {
	if len(raw) > maxCommandPluginInboundMessageBytes {
		return false, fmt.Errorf("command plugin: message %s exceeded %d bytes", msgType, maxCommandPluginInboundMessageBytes)
	}

	switch msgType {
	case pluginwire.MsgTypeDone:
		var msg pluginwire.DoneMsg
		if err := decodeCommandPluginMessage(msgType, raw, &msg); err != nil {
			return true, err
		}
		if msg.ExitCode < 0 || msg.ExitCode > 255 {
			c.warnf("command plugin returned out-of-range exit_code %d; clamping to 255", msg.ExitCode)
			msg.ExitCode = 255
		}
		if msg.ExitCode != 0 {
			return true, &ExitCodeError{Code: msg.ExitCode}
		}
		return true, nil
	case pluginwire.MsgTypeHTTPRequest:
		var msg pluginwire.HTTPRequestMsg
		if err := decodeCommandPluginMessage(msgType, raw, &msg); err != nil {
			return false, err
		}
		requestWG.Add(1)
		go func() {
			defer requestWG.Done()
			if err := c.handlePluginHTTPRequest(cmd, requestCtx, writer, msg); err != nil {
				_ = writer.WriteMessage(pluginwire.HTTPResponseMsg{
					Type:      pluginwire.MsgTypeHTTPResponse,
					RequestID: msg.RequestID,
					Error:     err.Error(),
				})
			}
		}()
		return false, nil
	case pluginwire.MsgTypeAPISpec:
		var msg pluginwire.APISpecMsg
		if err := decodeCommandPluginMessage(msgType, raw, &msg); err != nil {
			return false, err
		}
		return false, c.handlePluginAPISpec(requestCtx, cmd, writer, msg)
	case pluginwire.MsgTypeListAPIs:
		var msg pluginwire.ListAPIsMsg
		if err := decodeCommandPluginMessage(msgType, raw, &msg); err != nil {
			return false, err
		}
		return false, c.handlePluginListAPIs(writer, msg)
	case pluginwire.MsgTypeListProfiles:
		var msg pluginwire.ListProfilesMsg
		if err := decodeCommandPluginMessage(msgType, raw, &msg); err != nil {
			return false, err
		}
		return false, c.handlePluginListProfiles(writer, msg)
	case pluginwire.MsgTypeConfigRead:
		var msg pluginwire.ConfigReadMsg
		if err := decodeCommandPluginMessage(msgType, raw, &msg); err != nil {
			return false, err
		}
		return false, c.handlePluginConfigRead(writer, msg)
	case pluginwire.MsgTypePrompt:
		var msg pluginwire.PromptMsg
		if err := decodeCommandPluginMessage(msgType, raw, &msg); err != nil {
			return false, err
		}
		return false, c.handlePluginPrompt(cmd.Context(), writer, msg)
	case pluginwire.MsgTypeConfirm:
		var msg pluginwire.ConfirmMsg
		if err := decodeCommandPluginMessage(msgType, raw, &msg); err != nil {
			return false, err
		}
		return false, c.handlePluginConfirm(cmd.Context(), writer, msg)
	case pluginwire.MsgTypeResponse:
		var msg pluginwire.ResponseMsg
		if err := decodeCommandPluginMessage(msgType, raw, &msg); err != nil {
			return false, err
		}
		return false, c.handlePluginResponse(cmd, msg)
	case pluginwire.MsgTypeStdoutData:
		var msg pluginwire.StdoutDataMsg
		if err := decodeCommandPluginMessage(msgType, raw, &msg); err != nil {
			return false, err
		}
		if len(msg.Data) > maxCommandPluginDataBytes {
			return false, fmt.Errorf("command plugin: stdout data exceeded %d bytes", maxCommandPluginDataBytes)
		}
		if len(msg.Data) > 0 {
			_, _ = c.Stdout.Write(msg.Data)
		}
	case pluginwire.MsgTypeStderrData:
		var msg pluginwire.StderrDataMsg
		if err := decodeCommandPluginMessage(msgType, raw, &msg); err != nil {
			return false, err
		}
		if len(msg.Data) > maxCommandPluginDataBytes {
			return false, fmt.Errorf("command plugin: stderr data exceeded %d bytes", maxCommandPluginDataBytes)
		}
		if len(msg.Data) > 0 {
			_, _ = cmd.ErrOrStderr().Write(msg.Data)
		}
	case pluginwire.MsgTypeWarn:
		var msg pluginwire.WarnMsg
		if err := decodeCommandPluginMessage(msgType, raw, &msg); err != nil {
			return false, err
		}
		if msg.Text != "" {
			writeDiagnostic(cmd.ErrOrStderr(), diagnosticWarn, "warning", "%s", msg.Text)
		}
	case pluginwire.MsgTypeProgress:
		var msg pluginwire.ProgressMsg
		if err := decodeCommandPluginMessage(msgType, raw, &msg); err != nil {
			return false, err
		}
		if progress != nil && (msg.ID != "" || msg.Label != "") {
			handled, err := progress.Write(msg)
			if err != nil {
				writeDiagnostic(cmd.ErrOrStderr(), diagnosticWarn, "warning", "progress formatter unavailable: %v", err)
			}
			if handled {
				break
			}
		}
		if msg.Text != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), msg.Text)
		}
	case pluginwire.MsgTypeSpinner:
		var msg pluginwire.SpinnerMsg
		if err := decodeCommandPluginMessage(msgType, raw, &msg); err != nil {
			return false, err
		}
		if msg.Text != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), msg.Text)
		}
	case pluginwire.MsgTypeLog:
		var msg pluginwire.LogMsg
		if err := decodeCommandPluginMessage(msgType, raw, &msg); err != nil {
			return false, err
		}
		if msg.Text != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), msg.Text)
		}
	default:
		if msgType != "" {
			writeDiagnostic(cmd.ErrOrStderr(), diagnosticWarn, "warning", "unhandled plugin message type %q", msgType)
		}
	}
	return false, nil
}

func decodeCommandPluginMessage(msgType string, raw []byte, dst any) error {
	if err := pluginwire.DecMode.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("command plugin: decode %s: %w", msgType, err)
	}
	return nil
}

func (c *CLI) handlePluginHTTPRequest(cmd *cobra.Command, requestCtx context.Context, writer *commandPluginWriter, msg pluginwire.HTTPRequestMsg) error {
	method := msg.Method
	if method == "" {
		method = "GET"
	}

	opts, err := c.httpOptsFromFlags(cmd)
	if err != nil {
		return err
	}

	profileName := c.profileFromCmd(cmd)

	if msg.NoCache {
		opts.NoCache = true
	}
	if msg.CacheTTL > 0 {
		opts.Headers = append(opts.Headers, fmt.Sprintf("Cache-Control: max-age=%d", msg.CacheTTL))
	}
	for name, value := range msg.Headers {
		if value != "" {
			opts.Headers = append(opts.Headers, name+": "+value)
		}
	}
	if msg.Body != nil {
		opts.ContentType = msg.ContentType
	}

	reqCtx := requestCtx
	if msg.Timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(requestCtx, time.Duration(msg.Timeout)*time.Second)
		defer cancel()
	}

	prepared, err := c.prepareRequest(reqCtx, method, msg.URI, profileName, opts, msg.Body, nil, false, authHandlerOptions{}, nil, false, "")
	if err != nil {
		return writer.WriteMessage(pluginwire.HTTPResponseMsg{
			Type:      pluginwire.MsgTypeHTTPResponse,
			RequestID: msg.RequestID,
			Error:     err.Error(),
		})
	}
	defer c.closePreparedTransport(prepared)

	httpResp, err := c.sendPreparedRequest(reqCtx, method, prepared)
	if err != nil {
		return writer.WriteMessage(pluginwire.HTTPResponseMsg{
			Type:      pluginwire.MsgTypeHTTPResponse,
			RequestID: msg.RequestID,
			Error:     err.Error(),
		})
	}

	resp, err := c.normalizeHTTPResponse(httpResp, maxBodyBytes(cmd))
	if err != nil {
		return writer.WriteMessage(pluginwire.HTTPResponseMsg{
			Type:      pluginwire.MsgTypeHTTPResponse,
			RequestID: msg.RequestID,
			Error:     err.Error(),
		})
	}

	c.ensureBodyLinks(resp)
	body := resp.Body
	if msg.Filter != "" {
		doc := normalizedResponseDoc(resp)
		filtered, ferr := filter.Apply(msg.Filter, doc, filter.LangAuto)
		if ferr != nil {
			return writer.WriteMessage(pluginwire.HTTPResponseMsg{
				Type:      pluginwire.MsgTypeHTTPResponse,
				RequestID: msg.RequestID,
				Error:     ferr.Error(),
			})
		}
		body = filtered
	}

	return writer.WriteMessage(pluginwire.HTTPResponseMsg{
		Type:      pluginwire.MsgTypeHTTPResponse,
		RequestID: msg.RequestID,
		Status:    resp.Status,
		Headers:   resp.Headers,
		URL:       resp.URL,
		Links:     resp.Links,
		Body:      body,
	})
}

func (c *CLI) handlePluginResponse(cmd *cobra.Command, msg pluginwire.ResponseMsg) error {
	status := msg.Status
	if status == 0 {
		status = 200
	}
	resp := &output.Response{
		Proto:   "HTTP/1.1",
		Status:  status,
		Headers: msg.Headers,
		Body:    msg.Body,
	}
	return c.formatResponse(cmd, resp, nil)
}

func (c *CLI) handlePluginAPISpec(ctx context.Context, cmd *cobra.Command, writer *commandPluginWriter, msg pluginwire.APISpecMsg) error {
	profileName := msg.Profile
	if profileName == "" {
		if cmd != nil {
			profileName = c.profileFromCmd(cmd)
		} else {
			profileName = "default"
		}
	}
	if msg.Name == "" {
		return writer.WriteMessage(pluginwire.APISpecResponseMsg{
			Type:      pluginwire.MsgTypeAPISpecResponse,
			RequestID: msg.RequestID,
			Profile:   profileName,
			Error:     "missing api name",
		})
	}
	if c.cfg == nil || c.cfg.APIs == nil || c.cfg.APIs[msg.Name] == nil {
		return writer.WriteMessage(pluginwire.APISpecResponseMsg{
			Type:      pluginwire.MsgTypeAPISpecResponse,
			RequestID: msg.RequestID,
			Name:      msg.Name,
			Profile:   profileName,
			Error:     fmt.Sprintf("unknown API %q", msg.Name),
		})
	}
	apiCfg := c.cfg.APIs[msg.Name]

	s, err := spec.LoadFromCache(c.specCacheDir(), c.apiStateName(msg.Name), Version, apiCfg.SpecFiles, c.loaders)
	if err != nil {
		return writer.WriteMessage(pluginwire.APISpecResponseMsg{
			Type:      pluginwire.MsgTypeAPISpecResponse,
			RequestID: msg.RequestID,
			Name:      msg.Name,
			Profile:   profileName,
			Error:     err.Error(),
		})
	}
	if s == nil {
		s, err = c.discoverSpec(ctx, msg.Name)
		if err != nil {
			return writer.WriteMessage(pluginwire.APISpecResponseMsg{
				Type:      pluginwire.MsgTypeAPISpecResponse,
				RequestID: msg.RequestID,
				Name:      msg.Name,
				Profile:   profileName,
				Error:     err.Error(),
			})
		}
	}
	if s == nil || len(s.Raw) == 0 {
		return writer.WriteMessage(pluginwire.APISpecResponseMsg{
			Type:      pluginwire.MsgTypeAPISpecResponse,
			RequestID: msg.RequestID,
			Name:      msg.Name,
			Profile:   profileName,
			Error:     fmt.Sprintf("no spec available for %q", msg.Name),
		})
	}
	opSet, err := s.OperationSet(spec.OperationOptions{
		BaseURL:         effectiveProfileBaseURL(apiCfg, profileName),
		OperationBase:   effectiveOperationBase(apiCfg, profileName),
		ServerVariables: effectiveServerVariables(apiCfg, profileName),
	})
	if err != nil {
		return writer.WriteMessage(pluginwire.APISpecResponseMsg{
			Type:      pluginwire.MsgTypeAPISpecResponse,
			RequestID: msg.RequestID,
			Name:      msg.Name,
			Profile:   profileName,
			Error:     err.Error(),
		})
	}

	return writer.WriteMessage(pluginwire.APISpecResponseMsg{
		Type:        pluginwire.MsgTypeAPISpecResponse,
		RequestID:   msg.RequestID,
		Name:        msg.Name,
		Profile:     profileName,
		ContentType: s.ContentType,
		Raw:         s.Raw,
		Operations:  pluginOperationsFromSpec(opSet.Operations, effectiveOperationBase(apiCfg, profileName)),
	})
}

func pluginOperationsFromSpec(ops []spec.Operation, operationBase string) []pluginwire.APIOperation {
	if ops == nil {
		return nil
	}
	out := make([]pluginwire.APIOperation, 0, len(ops))
	for _, op := range ops {
		params := make([]pluginwire.APIParam, 0, len(op.Parameters))
		for _, p := range op.Parameters {
			if p.XCLI.Ignore {
				continue
			}
			params = append(params, pluginwire.APIParam{
				Name:             p.Name,
				In:               p.In,
				Required:         p.Required,
				Description:      p.Desc,
				Type:             p.Type,
				ItemType:         p.ItemType,
				Style:            p.Style,
				Explode:          p.Explode,
				AllowReserved:    p.AllowReserved,
				ContentMediaType: p.ContentMediaType,
				Schema:           cloneCommandPluginSchema(p.JSONSchema),
				SchemaDialect:    p.JSONSchemaDialect,
				Enum:             append([]string(nil), p.Enum...),
			})
		}
		requestSchema, requestSchemaDialect := operationRequestSchema(op)
		out = append(out, pluginwire.APIOperation{
			ID:                   operationCommandName(op, operationBase),
			Method:               op.Method,
			Path:                 op.Path,
			Summary:              op.Summary,
			Description:          op.Description,
			Deprecated:           op.Deprecated,
			Parameters:           params,
			HasBody:              op.HasBody,
			BodyRequired:         op.BodyRequired,
			RequestMediaType:     op.RequestMediaType,
			RequestSchema:        requestSchema,
			RequestSchemaDialect: requestSchemaDialect,
			MCPIgnore:            op.MCPIgnore,
		})
	}
	return out
}

func operationRequestSchema(op spec.Operation) (map[string]any, string) {
	if op.Help.Request != nil {
		return cloneCommandPluginSchema(op.Help.Request.JSONSchema), op.Help.Request.JSONSchemaDialect
	}
	for _, request := range op.Help.Requests {
		if mediaTypesMatch(request.MediaType, op.RequestMediaType) {
			return cloneCommandPluginSchema(request.JSONSchema), request.JSONSchemaDialect
		}
	}
	return nil, ""
}

func cloneCommandPluginSchema(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	data, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

func (c *CLI) handlePluginPrompt(ctx context.Context, writer *commandPluginWriter, msg pluginwire.PromptMsg) error {
	var value string
	var readErr error
	if msg.Hidden {
		value, readErr = c.Secret(ctx, msg.Message)
	} else {
		value, readErr = c.Prompt(ctx, msg.Message)
	}
	if readErr != nil {
		return writer.WriteMessage(pluginwire.PromptResponseMsg{
			Type:      pluginwire.MsgTypePromptResponse,
			RequestID: msg.RequestID,
			Error:     readErr.Error(),
		})
	}
	return writer.WriteMessage(pluginwire.PromptResponseMsg{
		Type:      pluginwire.MsgTypePromptResponse,
		RequestID: msg.RequestID,
		Value:     value,
	})
}

func (c *CLI) handlePluginConfirm(ctx context.Context, writer *commandPluginWriter, msg pluginwire.ConfirmMsg) error {
	confirmed, err := c.Confirm(ctx, msg.Message)
	if err != nil {
		return writer.WriteMessage(pluginwire.ConfirmResponseMsg{
			Type:      pluginwire.MsgTypeConfirmResponse,
			RequestID: msg.RequestID,
			Error:     err.Error(),
		})
	}
	return writer.WriteMessage(pluginwire.ConfirmResponseMsg{
		Type:      pluginwire.MsgTypeConfirmResponse,
		RequestID: msg.RequestID,
		Value:     confirmed,
	})
}

func (c *CLI) handlePluginConfigRead(writer *commandPluginWriter, msg pluginwire.ConfigReadMsg) error {
	reply := pluginwire.ConfigReadResponseMsg{Type: pluginwire.MsgTypeConfigReadResponse, RequestID: msg.RequestID}

	if msg.Plugin != "" && c.cfg != nil && c.cfg.Plugins != nil {
		if raw, ok := c.cfg.Plugins[msg.Plugin]; ok {
			var pluginCfg any
			if err := json.Unmarshal(raw, &pluginCfg); err == nil {
				reply.PluginConfig = pluginCfg
			}
		}
	}

	if c.cfg == nil || msg.API == "" {
		return writer.WriteMessage(reply)
	}
	apiCfg := c.cfg.APIs[msg.API]
	if apiCfg == nil {
		reply.Error = fmt.Sprintf("unknown API %q", msg.API)
		return writer.WriteMessage(reply)
	}
	baseURL := apiCfg.BaseURL
	if msg.Profile != "" {
		if prof := apiCfg.Profiles[msg.Profile]; prof != nil {
			if prof.BaseURL != "" {
				baseURL = prof.BaseURL
			}
			reply.Headers = prof.Headers
			reply.Query = prof.Query
		}
	}
	reply.BaseURL = baseURL
	return writer.WriteMessage(reply)
}

func (c *CLI) handlePluginListAPIs(writer *commandPluginWriter, msg pluginwire.ListAPIsMsg) error {
	var names []string
	if c.cfg != nil {
		names = make([]string, 0, len(c.cfg.APIs))
		for name := range c.cfg.APIs {
			names = append(names, name)
		}
		sort.Strings(names)
	}
	return writer.WriteMessage(pluginwire.ListAPIsResponseMsg{
		Type:      pluginwire.MsgTypeListAPIsResponse,
		RequestID: msg.RequestID,
		APIs:      names,
	})
}

func (c *CLI) handlePluginListProfiles(writer *commandPluginWriter, msg pluginwire.ListProfilesMsg) error {
	var profileNames []string
	if c.cfg != nil && msg.API != "" {
		if apiCfg := c.cfg.APIs[msg.API]; apiCfg != nil {
			profileNames = make([]string, 0, len(apiCfg.Profiles))
			for name := range apiCfg.Profiles {
				profileNames = append(profileNames, name)
			}
			sort.Strings(profileNames)
		}
	}
	return writer.WriteMessage(pluginwire.ListProfilesResponseMsg{
		Type:      pluginwire.MsgTypeListProfilesResponse,
		RequestID: msg.RequestID,
		API:       msg.API,
		Profiles:  profileNames,
	})
}
