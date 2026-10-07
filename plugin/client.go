package plugin

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// CommandClient is the plugin-side counterpart of the host command-plugin
// runner.  It wraps the host stdin/stdout pair and provides helpers for
// making authenticated HTTP calls, writing output, and signalling completion.
//
// Most command plugins should create one with NewCommandClient and use it
// for all communication with the host.
//
// Plugins that declare passthrough_stdio should register StdinDataHandler and
// StdinCloseHandler before calling Do(). These handlers are invoked for any
// stdin-data or stdin-close frames that arrive after Do starts the background
// response reader.
type CommandClient struct {
	dec     *Decoder
	out     io.Writer
	writeMu sync.Mutex

	readOnce          sync.Once
	stdinDispatchOnce sync.Once
	readErr           atomic.Value
	nextID            atomic.Uint64
	pending           sync.Map
	specs             sync.Map
	replies           sync.Map
	stdinEvents       chan stdinDispatchEvent

	// StdinDataHandler is called with each chunk of stdin data received from
	// the host while Do() is waiting for an http-response. Set this before
	// calling Do() when passthrough_stdio is active.
	StdinDataHandler func(data []byte)

	// StdinCloseHandler is called when the host signals that stdin has reached
	// EOF while Do() is waiting for an http-response.
	StdinCloseHandler func()
}

const stdinDispatchQueueSize = 64

type stdinDispatchEvent struct {
	data  []byte
	close bool
}

// NewCommandClient returns a CommandClient backed by the given reader/writer.
// Pass os.Stdin and os.Stdout from a plugin's main function.
// A single Decoder is created from in and reused for all reads. Call
// ReadMessage for startup messages before calling Do; after Do starts the
// background response reader, all host replies are routed through Do.
func NewCommandClient(in io.Reader, out io.Writer) *CommandClient {
	return &CommandClient{dec: NewDecoder(in), out: out}
}

// NewCommandClientFromDecoder returns a CommandClient that continues reading
// from an existing decoder. Use this when startup code has already consumed
// initial messages from the same input stream.
func NewCommandClientFromDecoder(dec *Decoder, out io.Writer) *CommandClient {
	return &CommandClient{dec: dec, out: out}
}

// ReadMessage reads one CBOR message from the host into v. Use this when the
// plugin needs to receive startup messages that are not covered by Do.
func (c *CommandClient) ReadMessage(v any) error {
	return c.dec.ReadMessage(v)
}

// Do sends one HTTP request to the host and blocks until the matching
// http-response arrives. While waiting, any stdin-data or stdin-close frames
// are dispatched to StdinDataHandler / StdinCloseHandler if registered.
//
// Do is safe to call concurrently. Requests without RequestID are assigned one
// automatically so replies can be routed back to the caller.
func (c *CommandClient) Do(req *HTTPRequestMsg) (*HTTPResponseMsg, error) {
	if req.Type == "" {
		req.Type = MsgTypeHTTPRequest
	}
	if req.RequestID == "" {
		req.RequestID = "req-" + strconv.FormatUint(c.nextID.Add(1), 10)
	}
	replyCh := make(chan commandReply, 1)
	if _, loaded := c.pending.LoadOrStore(req.RequestID, replyCh); loaded {
		return nil, fmt.Errorf("plugin: duplicate request_id %q", req.RequestID)
	}
	defer c.pending.Delete(req.RequestID)

	c.startReadLoop()
	if errValue := c.readErr.Load(); errValue != nil {
		return nil, errValue.(error)
	}
	if err := c.WriteMessage(req); err != nil {
		return nil, err
	}
	var timeout <-chan time.Time
	if req.Timeout > 0 {
		timeout = time.After(time.Duration(req.Timeout) * time.Second)
	}
	var reply commandReply
	select {
	case reply = <-replyCh:
	case <-timeout:
		return nil, fmt.Errorf("plugin: HTTP request %s timed out after %ds", req.RequestID, req.Timeout)
	}
	if reply.err != nil {
		return nil, reply.err
	}
	return &reply.resp, nil
}

// FetchAPISpec asks the host to load the OpenAPI spec or resolved operation
// metadata for a registered API name.
func (c *CommandClient) FetchAPISpec(name string) (*APISpecResponseMsg, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.FetchAPISpecContext(ctx, name, "")
}

// FetchAPISpecContext asks the host to load OpenAPI metadata for a registered
// API name and optional profile, returning when ctx is canceled if the host does
// not reply.
func (c *CommandClient) FetchAPISpecContext(ctx context.Context, name, profile string) (*APISpecResponseMsg, error) {
	var reply APISpecResponseMsg
	if err := c.roundTrip(ctx, MsgTypeAPISpecResponse, &APISpecMsg{
		Type:    MsgTypeAPISpec,
		Name:    name,
		Profile: profile,
	}, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

// ListAPIs asks the host for configured API names.
func (c *CommandClient) ListAPIs() (*ListAPIsResponseMsg, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.ListAPIsContext(ctx)
}

// ListAPIsContext asks the host for configured API names, returning when ctx is
// canceled if the host does not reply.
func (c *CommandClient) ListAPIsContext(ctx context.Context) (*ListAPIsResponseMsg, error) {
	var reply ListAPIsResponseMsg
	if err := c.roundTrip(ctx, MsgTypeListAPIsResponse, &ListAPIsMsg{Type: MsgTypeListAPIs}, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

// ListProfiles asks the host for profile names under api.
func (c *CommandClient) ListProfiles(api string) (*ListProfilesResponseMsg, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.ListProfilesContext(ctx, api)
}

// ListProfilesContext asks the host for profile names under api, returning when
// ctx is canceled if the host does not reply.
func (c *CommandClient) ListProfilesContext(ctx context.Context, api string) (*ListProfilesResponseMsg, error) {
	var reply ListProfilesResponseMsg
	if err := c.roundTrip(ctx, MsgTypeListProfilesResponse, &ListProfilesMsg{Type: MsgTypeListProfiles, API: api}, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

// ConfigRead asks the host for API/profile config and/or plugin config.
func (c *CommandClient) ConfigRead(api, profile, pluginName string) (*ConfigReadResponseMsg, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.ConfigReadContext(ctx, api, profile, pluginName)
}

// ConfigReadContext asks the host for API/profile config and/or plugin config,
// returning when ctx is canceled if the host does not reply.
func (c *CommandClient) ConfigReadContext(ctx context.Context, api, profile, pluginName string) (*ConfigReadResponseMsg, error) {
	var reply ConfigReadResponseMsg
	if err := c.roundTrip(ctx, MsgTypeConfigReadResponse, &ConfigReadMsg{
		Type:    MsgTypeConfigRead,
		API:     api,
		Profile: profile,
		Plugin:  pluginName,
	}, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

// Prompt asks the host to display message and read a value from the user.
func (c *CommandClient) Prompt(message string, hidden bool) (*PromptResponseMsg, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.PromptContext(ctx, message, hidden)
}

// PromptContext asks the host to display message and read a value from the
// user, returning when ctx is canceled if the host does not reply.
func (c *CommandClient) PromptContext(ctx context.Context, message string, hidden bool) (*PromptResponseMsg, error) {
	var reply PromptResponseMsg
	if err := c.roundTrip(ctx, MsgTypePromptResponse, &PromptMsg{Type: MsgTypePrompt, Message: message, Hidden: hidden}, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

// Confirm asks the host to display message and read a yes/no value.
func (c *CommandClient) Confirm(message string) (*ConfirmResponseMsg, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.ConfirmContext(ctx, message)
}

// ConfirmContext asks the host to display message and read a yes/no value,
// returning when ctx is canceled if the host does not reply.
func (c *CommandClient) ConfirmContext(ctx context.Context, message string) (*ConfirmResponseMsg, error) {
	var reply ConfirmResponseMsg
	if err := c.roundTrip(ctx, MsgTypeConfirmResponse, &ConfirmMsg{Type: MsgTypeConfirm, Message: message}, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

// Select asks the host to display a single-choice picker.
func (c *CommandClient) Select(message string, options []SelectOption) (*SelectResponseMsg, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.SelectContext(ctx, message, options)
}

// SelectContext asks the host to display a single-choice picker, returning
// when ctx is canceled if the host does not reply.
func (c *CommandClient) SelectContext(ctx context.Context, message string, options []SelectOption) (*SelectResponseMsg, error) {
	var reply SelectResponseMsg
	if err := c.roundTrip(ctx, MsgTypeSelectResponse, &SelectMsg{Type: MsgTypeSelect, Message: message, Options: options}, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

// Response asks the host to format and display response data using the same
// output machinery as regular Restish responses.
func (c *CommandClient) Response(status int, headers map[string][]string, body any) error {
	return c.WriteMessage(ResponseMsg{Type: MsgTypeResponse, Status: status, Headers: headers, Body: body})
}

type commandReply struct {
	resp HTTPResponseMsg
	err  error
}

type specReply struct {
	resp APISpecResponseMsg
	err  error
}

type clientReply struct {
	raw []byte
	err error
}

func (c *CommandClient) startReadLoop() {
	c.readOnce.Do(func() {
		go c.readLoop()
	})
}

func (c *CommandClient) readLoop() {
	for {
		raw, err := c.dec.ReadRaw()
		if err != nil {
			c.failPending(err)
			return
		}
		msgType := MessageType(raw)
		switch msgType {
		case MsgTypeHTTPResponse:
			var reply HTTPResponseMsg
			if err := DecMode.Unmarshal(raw, &reply); err != nil {
				c.failPending(fmt.Errorf("plugin: decode http-response: %w", err))
				return
			}
			c.deliverHTTPResponse(reply)
		case MsgTypeAPISpecResponse:
			var reply APISpecResponseMsg
			if err := DecMode.Unmarshal(raw, &reply); err != nil {
				c.failPending(fmt.Errorf("plugin: decode api-spec-response: %w", err))
				return
			}
			if reply.RequestID != "" {
				c.deliverClientReply(MsgTypeAPISpecResponse, reply.RequestID, raw)
			} else {
				c.deliverAPISpecResponse(reply)
			}
		case MsgTypeListAPIsResponse:
			c.deliverResponseByRequestID(MsgTypeListAPIsResponse, raw)
		case MsgTypeListProfilesResponse:
			c.deliverResponseByRequestID(MsgTypeListProfilesResponse, raw)
		case MsgTypeConfigReadResponse:
			c.deliverResponseByRequestID(MsgTypeConfigReadResponse, raw)
		case MsgTypePromptResponse:
			c.deliverResponseByRequestID(MsgTypePromptResponse, raw)
		case MsgTypeConfirmResponse:
			c.deliverResponseByRequestID(MsgTypeConfirmResponse, raw)
		case MsgTypeSelectResponse:
			c.deliverResponseByRequestID(MsgTypeSelectResponse, raw)
		case MsgTypeStdinData:
			if c.StdinDataHandler != nil {
				var msg StdinDataMsg
				if err := DecMode.Unmarshal(raw, &msg); err != nil {
					c.failPending(fmt.Errorf("plugin: decode stdin-data: %w", err))
					return
				}
				if err := c.dispatchStdinEvent(stdinDispatchEvent{data: msg.Data}); err != nil {
					c.failPending(err)
					return
				}
			}
		case MsgTypeStdinClose:
			if c.StdinCloseHandler != nil {
				if err := c.dispatchStdinEvent(stdinDispatchEvent{close: true}); err != nil {
					c.failPending(err)
					return
				}
			}
		default:
			continue
		}
	}
}

func (c *CommandClient) dispatchStdinEvent(event stdinDispatchEvent) error {
	c.stdinDispatchOnce.Do(func() {
		c.stdinEvents = make(chan stdinDispatchEvent, stdinDispatchQueueSize)
		go c.runStdinDispatcher(c.stdinEvents)
	})
	select {
	case c.stdinEvents <- event:
		return nil
	default:
		return fmt.Errorf("plugin: stdin handler queue full; consumer is not reading stdin events fast enough")
	}
}

func (c *CommandClient) runStdinDispatcher(events <-chan stdinDispatchEvent) {
	for event := range events {
		if event.close {
			if c.StdinCloseHandler != nil {
				c.StdinCloseHandler()
			}
			continue
		}
		if c.StdinDataHandler != nil {
			c.StdinDataHandler(event.data)
		}
	}
}

func (c *CommandClient) deliverHTTPResponse(reply HTTPResponseMsg) {
	if reply.RequestID != "" {
		if ch, ok := c.pending.LoadAndDelete(reply.RequestID); ok {
			replyCh := ch.(chan commandReply)
			select {
			case replyCh <- commandReply{resp: reply}:
			default:
			}
			close(replyCh)
		}
	}
}

func (c *CommandClient) deliverAPISpecResponse(reply APISpecResponseMsg) {
	key := specRequestKey(reply.Name, reply.Profile)
	if ch, ok := c.specs.LoadAndDelete(key); ok {
		replyCh := ch.(chan specReply)
		select {
		case replyCh <- specReply{resp: reply}:
		default:
		}
		close(replyCh)
	}
}

func (c *CommandClient) deliverResponseByRequestID(msgType string, raw []byte) {
	var envelope struct {
		RequestID string `cbor:"request_id"`
	}
	if err := DecMode.Unmarshal(raw, &envelope); err != nil || envelope.RequestID == "" {
		return
	}
	c.deliverClientReply(msgType, envelope.RequestID, raw)
}

func (c *CommandClient) deliverClientReply(msgType, requestID string, raw []byte) {
	key := replyRequestKey(msgType, requestID)
	if ch, ok := c.replies.LoadAndDelete(key); ok {
		replyCh := ch.(chan clientReply)
		select {
		case replyCh <- clientReply{raw: raw}:
		default:
		}
		close(replyCh)
	}
}

func specRequestKey(name, profile string) string {
	if profile == "" {
		profile = "default"
	}
	return name + "\x00" + profile
}

func (c *CommandClient) failPending(err error) {
	c.readErr.Store(err)
	c.pending.Range(func(key, value any) bool {
		c.pending.Delete(key)
		ch := value.(chan commandReply)
		select {
		case ch <- commandReply{err: err}:
		default:
		}
		close(ch)
		return true
	})
	c.specs.Range(func(key, value any) bool {
		c.specs.Delete(key)
		ch := value.(chan specReply)
		select {
		case ch <- specReply{err: err}:
		default:
		}
		close(ch)
		return true
	})
	c.replies.Range(func(key, value any) bool {
		c.replies.Delete(key)
		ch := value.(chan clientReply)
		select {
		case ch <- clientReply{err: err}:
		default:
		}
		close(ch)
		return true
	})
}

func (c *CommandClient) roundTrip(ctx context.Context, responseType string, outbound any, reply any) error {
	requestID := "req-" + strconv.FormatUint(c.nextID.Add(1), 10)
	setRequestID(outbound, requestID)
	replyCh := make(chan clientReply, 1)
	key := replyRequestKey(responseType, requestID)
	if _, loaded := c.replies.LoadOrStore(key, replyCh); loaded {
		return fmt.Errorf("plugin: duplicate request_id %q", requestID)
	}
	defer c.replies.Delete(key)

	c.startReadLoop()
	if errValue := c.readErr.Load(); errValue != nil {
		return errValue.(error)
	}
	if err := c.WriteMessage(outbound); err != nil {
		return err
	}
	var r clientReply
	select {
	case r = <-replyCh:
	case <-ctx.Done():
		return fmt.Errorf("plugin: %s request %s timed out or was canceled: %w", responseType, requestID, ctx.Err())
	}
	if r.err != nil {
		return r.err
	}
	if len(r.raw) == 0 {
		return fmt.Errorf("plugin: %s request %s closed without reply", responseType, requestID)
	}
	if err := DecMode.Unmarshal(r.raw, reply); err != nil {
		return fmt.Errorf("plugin: decode %s: %w", responseType, err)
	}
	return nil
}

func replyRequestKey(msgType, requestID string) string {
	return msgType + "\x00" + requestID
}

func setRequestID(msg any, requestID string) {
	switch m := msg.(type) {
	case *ListAPIsMsg:
		m.RequestID = requestID
	case *ListProfilesMsg:
		m.RequestID = requestID
	case *ConfigReadMsg:
		m.RequestID = requestID
	case *PromptMsg:
		m.RequestID = requestID
	case *ConfirmMsg:
		m.RequestID = requestID
	case *SelectMsg:
		m.RequestID = requestID
	case *APISpecMsg:
		m.RequestID = requestID
	}
}

// WriteStdout writes data to the user's terminal via the host.
func (c *CommandClient) WriteStdout(data []byte) error {
	return c.WriteMessage(StdoutDataMsg{Type: MsgTypeStdoutData, Data: append([]byte(nil), data...)})
}

// WriteStderr writes data to the user's terminal stderr via the host.
func (c *CommandClient) WriteStderr(data []byte) error {
	return c.WriteMessage(StderrDataMsg{Type: MsgTypeStderrData, Data: append([]byte(nil), data...)})
}

// Warn sends a warning message to the host, which displays it to the user
// through whatever UI mechanism it uses.
func (c *CommandClient) Warn(text string) error {
	return c.WriteMessage(WarnMsg{Type: MsgTypeWarn, Text: text})
}

// Progress sends an informational progress message to the host.
func (c *CommandClient) Progress(text string) error {
	return c.WriteMessage(ProgressMsg{Type: MsgTypeProgress, Text: text})
}

// Done signals that the plugin has finished.  exitCode 0 means success; any
// other value causes the host to exit with that code.
func (c *CommandClient) Done(exitCode int) error {
	return c.WriteMessage(DoneMsg{Type: MsgTypeDone, ExitCode: exitCode})
}

// WriteMessage serialises v as a CBOR data item and sends it to the host.
// It is safe to call from multiple goroutines.
func (c *CommandClient) WriteMessage(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return WriteMessage(c.out, v)
}

// StdoutWriter returns an io.Writer that routes writes through WriteStdout.
func (c *CommandClient) StdoutWriter() io.Writer {
	return &clientStreamWriter{fn: c.WriteStdout}
}

// StderrWriter returns an io.Writer that routes writes through WriteStderr.
func (c *CommandClient) StderrWriter() io.Writer {
	return &clientStreamWriter{fn: c.WriteStderr}
}

type clientStreamWriter struct {
	fn func([]byte) error
}

func (w *clientStreamWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := w.fn(p); err != nil {
		return 0, err
	}
	return len(p), nil
}
