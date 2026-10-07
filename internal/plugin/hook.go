package plugin

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rest-sh/restish/v2/internal/procutil"
	"github.com/rest-sh/restish/v2/internal/secrets"
	pluginwire "github.com/rest-sh/restish/v2/plugin"
)

const (
	maxHookOutputBytes = 16 << 20
	maxHookStderrBytes = 64 << 10
)

// HookTimeout returns the effective timeout for hookName from the manifest,
// using a default of 5 minutes for "auth" and 30 seconds for all other hooks.
func HookTimeout(m Manifest, hookName string) time.Duration {
	if d, ok := m.HookTimeouts[hookName]; ok && d > 0 {
		return d
	}
	if hookName == "auth" {
		return 5 * time.Minute
	}
	return 30 * time.Second
}

// callHookRaw spawns the plugin at path, writes in as a CBOR message to
// stdin, and returns all bytes written to stdout on success. Non-zero plugin
// exit is returned as an error together with any text on stderr.
func callHookRaw(ctx context.Context, path string, timeout time.Duration, in any) ([]byte, error) {
	var stdin bytes.Buffer
	if err := pluginwire.WriteMessage(&stdin, in); err != nil {
		return nil, fmt.Errorf("hook %s: encode: %w", filepath.Base(path), err)
	}

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stdout := &limitedBuffer{limit: maxHookOutputBytes + 1}
	stderr := &limitedBuffer{limit: maxHookStderrBytes}
	cmd := exec.CommandContext(ctx, path)
	procutil.ConfigureCommandTreeKill(ctx, cmd)
	cmd.Stdin = &stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return nil, fmt.Errorf("hook %s: exec: %w\n  plugin stderr: %s", filepath.Base(path), ctxErr, secrets.RedactDiagnosticText(msg))
			}
			return nil, fmt.Errorf("hook %s: exec: %w", filepath.Base(path), ctxErr)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("hook %s: exec: %w\n  plugin stderr: %s", filepath.Base(path), err, secrets.RedactDiagnosticText(msg))
		}
		return nil, fmt.Errorf("hook %s: exec: %w", filepath.Base(path), err)
	}
	if stdout.Len() > maxHookOutputBytes || stdout.Truncated() {
		return nil, fmt.Errorf("hook %s: output exceeded %d bytes", filepath.Base(path), maxHookOutputBytes)
	}
	return stdout.Bytes(), nil
}

// FormatterStream is a long-lived formatter plugin process that receives
// sequential CBOR requests on stdin and writes formatted bytes directly to the
// provided stdout writer.
type FormatterStream struct {
	path   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	output *pluginwire.Decoder
	stderr limitedBuffer
}

// StartFormatterStream starts a formatter plugin subprocess, wires its stdout
// to w, sends the initial request, and returns a handle that can send
// additional stream messages before Close waits for plugin exit.
func StartFormatterStream(ctx context.Context, path string, w io.Writer, in any) (*FormatterStream, error) {
	return startFormatterStream(ctx, path, w, in, false)
}

// StartFormatterOutputStream starts a formatter whose stdout is a stream of
// FormatterOutput messages rather than raw bytes.
func StartFormatterOutputStream(ctx context.Context, path string) (*FormatterStream, error) {
	return startFormatterStream(ctx, path, nil, nil, true)
}

func startFormatterStream(ctx context.Context, path string, w io.Writer, in any, framed bool) (*FormatterStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, path)
	procutil.ConfigureCommandTreeKill(ctx, cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("hook %s: stdin: %w", filepath.Base(path), err)
	}

	stream := &FormatterStream{
		path:  path,
		cmd:   cmd,
		stdin: stdin,
		stderr: limitedBuffer{
			limit: maxHookStderrBytes,
		},
	}
	if framed {
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			_ = stdin.Close()
			return nil, fmt.Errorf("hook %s: stdout: %w", filepath.Base(path), err)
		}
		stream.output = pluginwire.NewDecoder(stdout)
	} else {
		cmd.Stdout = w
	}
	cmd.Stderr = &stream.stderr

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("hook %s: start: %w", filepath.Base(path), err)
	}

	if in != nil {
		if err := pluginwire.WriteMessage(stdin, in); err != nil {
			_ = stdin.Close()
			_ = cmd.Wait()
			return nil, fmt.Errorf("hook %s: encode: %w", filepath.Base(path), err)
		}
	}

	return stream, nil
}

// Send writes one additional CBOR message to the formatter plugin.
func (s *FormatterStream) Send(in any) error {
	if err := pluginwire.WriteMessage(s.stdin, in); err != nil {
		return fmt.Errorf("hook %s: encode: %w", filepath.Base(s.path), err)
	}
	return nil
}

// SendWithTimeout writes a formatter request and kills the plugin if it stops
// reading stdin.
func (s *FormatterStream) SendWithTimeout(in any, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- s.Send(in) }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		_ = s.Abort()
		<-done
		return fmt.Errorf("hook %s: formatter did not accept request within %s", filepath.Base(s.path), timeout)
	}
}

// Abort kills a formatter process so blocked I/O can unwind before Close reaps
// it.
func (s *FormatterStream) Abort() error {
	return s.cmd.Process.Kill()
}

// Receive reads one FormatterOutput message from a framed formatter stream.
func (s *FormatterStream) Receive(out *pluginwire.FormatterOutput) error {
	if s.output == nil {
		return fmt.Errorf("hook %s: formatter stdout is not framed", filepath.Base(s.path))
	}
	if err := s.output.ReadMessage(out); err != nil {
		return fmt.Errorf("hook %s: decode formatter output: %w", filepath.Base(s.path), err)
	}
	return nil
}

// formatterCloseTimeout is the grace period given to a formatter plugin to
// exit after its stdin is closed before it is killed.
const formatterCloseTimeout = 10 * time.Second

// Close closes plugin stdin and waits up to formatterCloseTimeout for the
// plugin to exit. If the plugin does not exit in time it is killed.
func (s *FormatterStream) Close() error {
	if s.stdin != nil {
		if err := s.stdin.Close(); err != nil {
			return fmt.Errorf("hook %s: close stdin: %w", filepath.Base(s.path), err)
		}
		s.stdin = nil
	}

	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			if msg := strings.TrimSpace(s.stderr.String()); msg != "" {
				return fmt.Errorf("hook %s: exec: %w\n  plugin stderr: %s", filepath.Base(s.path), err, secrets.RedactDiagnosticText(msg))
			}
			return fmt.Errorf("hook %s: exec: %w", filepath.Base(s.path), err)
		}
	case <-time.After(formatterCloseTimeout):
		_ = s.cmd.Process.Kill()
		<-done
		return fmt.Errorf("hook %s: plugin did not exit within %s; killed", filepath.Base(s.path), formatterCloseTimeout)
	}
	return nil
}

// CallHook spawns the plugin at path, writes in as a CBOR message to the
// plugin's stdin, reads one CBOR reply from stdout, and unmarshals it into out
// (which must be a pointer).
//
// The plugin must exit 0 for the call to succeed; a non-zero exit is returned
// as an error along with any text the plugin wrote to stderr. The plugin has
// 30 seconds to respond before it is killed. Use CallHookWithTimeout to
// override the deadline.
func CallHook(path string, in, out any) error {
	return CallHookContext(context.Background(), path, in, out)
}

// CallHookContext is like CallHook but derives the plugin deadline from ctx.
func CallHookContext(ctx context.Context, path string, in, out any) error {
	return CallHookWithTimeoutContext(ctx, path, 30*time.Second, in, out)
}

// CallHookWithTimeout is like CallHook but uses the supplied deadline.
func CallHookWithTimeout(path string, timeout time.Duration, in, out any) error {
	return CallHookWithTimeoutContext(context.Background(), path, timeout, in, out)
}

// CallHookWithTimeoutContext is like CallHookWithTimeout but derives the plugin
// deadline from ctx.
func CallHookWithTimeoutContext(ctx context.Context, path string, timeout time.Duration, in, out any) error {
	data, err := callHookRaw(ctx, path, timeout, in)
	if err != nil {
		return err
	}
	if err := pluginwire.ReadMessage(bytes.NewReader(data), out); err != nil {
		return fmt.Errorf("hook %s: decode: %w", filepath.Base(path), err)
	}
	return nil
}
