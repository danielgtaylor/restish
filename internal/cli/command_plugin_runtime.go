package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/rest-sh/restish/v2/internal/procutil"
	pluginwire "github.com/rest-sh/restish/v2/plugin"
	"github.com/spf13/cobra"
)

type commandPluginWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (w *commandPluginWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}

func (w *commandPluginWriter) WriteMessage(v any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return pluginwire.WriteMessage(w.w, v)
}

type lineBufferedCommandPluginStderr struct {
	mu      sync.Mutex
	display io.Writer
	capture io.Writer
	buf     bytes.Buffer
}

func (w *lineBufferedCommandPluginStderr) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.capture != nil {
		_, _ = w.capture.Write(p)
	}
	start := 0
	for i, b := range p {
		if b != '\n' {
			continue
		}
		w.buf.Write(p[start : i+1])
		_, _ = w.display.Write(w.buf.Bytes())
		w.buf.Reset()
		start = i + 1
	}
	if start < len(p) {
		w.buf.Write(p[start:])
	}
	return len(p), nil
}

func (w *lineBufferedCommandPluginStderr) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len() == 0 {
		return
	}
	_, _ = w.display.Write(w.buf.Bytes())
	w.buf.Reset()
}

type commandPluginWaiter struct {
	cmd    *exec.Cmd
	done   chan error
	mu     sync.Mutex
	err    error
	waited bool
}

func newCommandPluginWaiter(cmd *exec.Cmd) *commandPluginWaiter {
	w := &commandPluginWaiter{cmd: cmd, done: make(chan error, 1)}
	go func() {
		w.done <- cmd.Wait()
	}()
	return w
}

func (w *commandPluginWaiter) Wait(grace time.Duration) error {
	w.mu.Lock()
	if w.waited {
		err := w.err
		w.mu.Unlock()
		return err
	}
	w.mu.Unlock()

	timer := time.NewTimer(grace)
	defer timer.Stop()

	var err error
	select {
	case err = <-w.done:
	case <-timer.C:
		if w.cmd.Process != nil {
			_ = w.cmd.Process.Kill()
		}
		err = <-w.done
	}
	w.mu.Lock()
	w.err = err
	w.waited = true
	w.mu.Unlock()
	return err
}

func (c *CLI) runCommandPlugin(cmd *cobra.Command, pluginPath string, decl pluginwire.CommandDecl, args []string) error {
	syncErr := &commandPluginWriter{w: cmd.ErrOrStderr()}
	cmd.SetErr(syncErr)
	args = stripHostPersistentFlags(cmd, args)

	proc := exec.CommandContext(cmd.Context(), pluginPath, append(terminalContextFlags(c), args...)...)
	procutil.ConfigureCommandTreeKill(cmd.Context(), proc)
	var pluginStderr bytes.Buffer
	stderrWriter := &lineBufferedCommandPluginStderr{
		display: cmd.ErrOrStderr(),
		capture: &limitedWriter{w: &pluginStderr, limit: maxCommandPluginStderrBytes},
	}
	proc.Stderr = stderrWriter

	stdinPipe, err := proc.StdinPipe()
	if err != nil {
		return fmt.Errorf("command plugin: stdin pipe: %w", err)
	}
	stdoutPipe, err := proc.StdoutPipe()
	if err != nil {
		_ = stdinPipe.Close()
		return fmt.Errorf("command plugin: stdout pipe: %w", err)
	}
	if err := proc.Start(); err != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		return fmt.Errorf("command plugin: start: %w", err)
	}
	waiter := newCommandPluginWaiter(proc)
	cancelWatchDone := make(chan struct{})
	go func() {
		select {
		case <-cmd.Context().Done():
			_ = stdinPipe.Close()
			_ = stdoutPipe.Close()
		case <-cancelWatchDone:
		}
	}()
	cleanupStartFailure := func(cause error) error {
		close(cancelWatchDone)
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		if proc.Process != nil {
			_ = proc.Process.Kill()
		}
		_ = waiter.Wait(commandPluginShutdownGrace())
		stderrWriter.Flush()
		return cause
	}

	writer := &commandPluginWriter{w: stdinPipe}
	initMsg := pluginwire.InitMsg{
		Type:    pluginwire.MsgTypeInit,
		Command: decl.Name,
		Args:    args,
	}
	if err := writer.WriteMessage(initMsg); err != nil {
		return cleanupStartFailure(fmt.Errorf("command plugin: send init: %w", err))
	}

	// stopCh is closed when the command loop exits, signalling streamPluginStdin
	// to stop forwarding. For TTY stdin the inner reader goroutine may remain
	// briefly blocked until the user interacts, but it will exit promptly once
	// stdinPipe is closed and the next write fails.
	stopCh := make(chan struct{})
	if decl.PassthroughStdio {
		go c.streamPluginStdin(writer, stopCh)
	}

	var loopErr error
	doneReceived := false
	var requestWG sync.WaitGroup
	var interactionMu sync.Mutex
	requestCtx, cancelRequests := context.WithCancel(cmd.Context())
	defer cancelRequests()
	dec := pluginwire.NewDecoder(stdoutPipe)
	for {
		raw, err := dec.ReadRaw()
		if err != nil {
			if ctxErr := cmd.Context().Err(); ctxErr != nil {
				if excerpt := strings.TrimSpace(pluginStderr.String()); excerpt != "" {
					loopErr = fmt.Errorf("command plugin %s canceled: %w: stderr: %s", filepath.Base(pluginPath), ctxErr, redactDiagnosticSecretText(excerpt))
				} else {
					loopErr = fmt.Errorf("command plugin %s canceled: %w", filepath.Base(pluginPath), ctxErr)
				}
			} else if isEOFLike(err) {
				loopErr = fmt.Errorf("command plugin %s: process died unexpectedly", filepath.Base(pluginPath))
			} else {
				loopErr = fmt.Errorf("command plugin %s: read message: %w", filepath.Base(pluginPath), err)
			}
			break
		}

		done, err := c.handleCommandPluginMessage(cmd, requestCtx, writer, &requestWG, &interactionMu, decl.PassthroughStdio, pluginwire.MessageType(raw), raw)
		if err != nil {
			loopErr = err
			break
		}
		if done {
			doneReceived = pluginwire.MessageType(raw) == pluginwire.MsgTypeDone
			break
		}
	}

	close(cancelWatchDone)
	close(stopCh)
	cancelRequests()
	stderrWriter.Flush()
	if loopErr != nil {
		_ = stdinPipe.Close()
		requestWG.Wait()
	} else {
		requestWG.Wait()
		_ = stdinPipe.Close()
	}
	waitErr := waiter.Wait(commandPluginShutdownGrace())
	if loopErr == nil && waitErr != nil && !doneReceived {
		loopErr = fmt.Errorf("command plugin %s: wait: %w", filepath.Base(pluginPath), waitErr)
	}
	// Warn when the plugin exits non-zero after sending Done — this is not a CLI
	// error but may indicate a plugin bug (e.g. panic in a deferred cleanup).
	if doneReceived && waitErr != nil {
		c.warnf("command plugin %s exited with error after Done: %v", filepath.Base(pluginPath), waitErr)
	}
	return loopErr
}

func commandPluginShutdownGrace() time.Duration {
	if value := strings.TrimSpace(os.Getenv("RSH_COMMAND_PLUGIN_SHUTDOWN_GRACE")); value != "" {
		if d, err := time.ParseDuration(value); err == nil && d > 0 {
			return d
		}
	}
	return 5 * time.Second
}

// streamPluginStdin forwards c.Stdin to the command plugin as "stdin-data"
// messages until stdin closes, a write error occurs, or done is closed.
func (c *CLI) streamPluginStdin(writer *commandPluginWriter, done <-chan struct{}) {
	reader := newCancelableStdinReader(c.Stdin, done)
	buf := make([]byte, 32*1024)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			if writeErr := writer.WriteMessage(pluginwire.StdinDataMsg{
				Type: pluginwire.MsgTypeStdinData,
				Data: data,
			}); writeErr != nil {
				return
			}
		}
		if errors.Is(err, errStdinReadCanceled) {
			return
		}
		if errors.Is(err, io.EOF) {
			_ = writer.WriteMessage(pluginwire.StdinCloseMsg{Type: pluginwire.MsgTypeStdinClose})
			return
		}
		if err != nil {
			return
		}
	}
}

var errStdinReadCanceled = errors.New("stdin read canceled")

type stdinReadDeadliner interface {
	SetReadDeadline(time.Time) error
}

type cancelableStdinReader struct {
	r          io.Reader
	done       <-chan struct{}
	deadliner  stdinReadDeadliner
	deadlineOK bool
}

func newCancelableStdinReader(r io.Reader, done <-chan struct{}) *cancelableStdinReader {
	dr, ok := r.(stdinReadDeadliner)
	return &cancelableStdinReader{
		r:          r,
		done:       done,
		deadliner:  dr,
		deadlineOK: ok,
	}
}

func (r *cancelableStdinReader) Read(p []byte) (int, error) {
	if !r.deadlineOK {
		select {
		case <-r.done:
			return 0, errStdinReadCanceled
		default:
		}
		return r.r.Read(p)
	}
	for {
		select {
		case <-r.done:
			_ = r.deadliner.SetReadDeadline(time.Time{})
			return 0, errStdinReadCanceled
		default:
		}
		if err := r.deadliner.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			r.deadlineOK = false
			return r.Read(p)
		}
		n, err := r.r.Read(p)
		if err == nil || n > 0 || errors.Is(err, io.EOF) {
			_ = r.deadliner.SetReadDeadline(time.Time{})
			return n, err
		}
		if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
			continue
		}
		_ = r.deadliner.SetReadDeadline(time.Time{})
		return n, err
	}
}
func isEOFLike(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var syntaxErr *cbor.SyntaxError
	if errors.As(err, &syntaxErr) {
		s := strings.ToLower(syntaxErr.Error())
		return strings.Contains(s, "unexpected eof") || strings.Contains(s, "truncated")
	}
	// Last resort for library and platform wrappers that do not preserve a
	// concrete sentinel. Keep this narrow so ordinary protocol errors are not
	// mistaken for plugin death.
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unexpected eof") || strings.Contains(s, "truncated") || strings.Contains(s, "broken pipe") || strings.Contains(s, "file already closed")
}

func stripHostPersistentFlags(cmd *cobra.Command, args []string) []string {
	if cmd == nil || cmd.Root() == nil || len(args) == 0 {
		return args
	}
	flags := cmd.Root().PersistentFlags()
	if flags == nil {
		return args
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		token := args[i]
		if token == "--" {
			out = append(out, args[i:]...)
			break
		}
		if strings.HasPrefix(token, "--") && token != "--" {
			nameValue := strings.TrimPrefix(token, "--")
			name, _, hasValue := strings.Cut(nameValue, "=")
			if flag := flags.Lookup(name); flag != nil {
				if !hasValue && flag.NoOptDefVal == "" && i+1 < len(args) {
					i++
				}
				continue
			}
		}
		out = append(out, token)
	}
	return out
}
