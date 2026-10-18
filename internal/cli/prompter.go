package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/rest-sh/restish/v2/internal/output"
	"golang.org/x/term"
)

var promptOpenTTY = openPromptTTY

func openPromptTTY() (*os.File, error) {
	if runtime.GOOS == "windows" {
		return os.OpenFile("CONIN$", os.O_RDWR, 0)
	}
	return os.Open("/dev/tty")
}

// Prompt implements Prompter by reading a visible input line from the user.
func (c *CLI) Prompt(ctx context.Context, label string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if c.hooks.PromptFunc != nil {
		return c.hooks.PromptFunc(ctx, label)
	}
	src, cleanup := c.promptSource()
	defer cleanup()
	return readPromptValue(label, src, c.Stderr, false)
}

// Secret implements Prompter by reading a password/token without echo.
func (c *CLI) Secret(ctx context.Context, label string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if c.hooks.SecretFunc != nil {
		return c.hooks.SecretFunc(ctx, label)
	}
	src, cleanup := c.promptSource()
	defer cleanup()
	return readPromptValue(label, src, c.Stderr, true)
}

// Confirm implements Prompter by reading a yes/no confirmation.
//
// Rules:
//   - "y" or "yes" (case-insensitive) → true
//   - "n", "no", or any other non-empty input → false
//   - Empty input (Enter) → true only when stdin is an interactive TTY
//   - EOF → false (safe default for piped/scripted invocations)
func (c *CLI) Confirm(ctx context.Context, label string) (bool, error) {
	return c.confirmDefault(ctx, label, true)
}

func (c *CLI) confirm(ctx context.Context) (bool, error) {
	return c.confirmDefault(ctx, "", false)
}

func (c *CLI) confirmDefault(ctx context.Context, label string, defaultYes bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if label != "" {
		fmt.Fprint(c.Stderr, label)
	}

	src, cleanup := c.promptSource()
	defer cleanup()
	reader := bufio.NewReader(src)
	line, err := reader.ReadString('\n')
	if errors.Is(err, io.EOF) && line == "" {
		return false, nil
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("confirm: %w", err)
	}
	answer := strings.TrimSpace(strings.ToLower(line))
	if answer == "" {
		if output.IsTerminalReader(src) {
			return defaultYes, nil
		}
		return false, nil
	}
	return answer == "y" || answer == "yes", nil
}

// promptSource returns the I/O reader to use for interactive prompts.
// Priority:
//  1. hooks.PassReader (set in tests to avoid TTY dependency)
//  2. /dev/tty when c.Stdin is not a terminal (stdin may be piped but we still
//     need to reach the user's keyboard for interactive prompts)
//  3. c.Stdin as a last resort
func (c *CLI) promptSource() (io.Reader, func()) {
	if c.hooks.PassReader != nil {
		return c.hooks.PassReader, func() {}
	}
	if !output.IsTerminalReader(c.Stdin) {
		if f, err := promptOpenTTY(); err == nil {
			return f, func() { _ = f.Close() }
		}
	}
	return c.Stdin, func() {}
}

func (c *CLI) canPromptInteractively() bool {
	if c.hooks.PromptFunc != nil {
		return true
	}
	return c.canPromptSecretInteractively()
}

func (c *CLI) canPromptSecretInteractively() bool {
	if c.hooks.SecretFunc != nil || c.hooks.PassReader != nil {
		return true
	}
	if output.IsTerminalReader(c.Stdin) {
		return true
	}
	f, err := promptOpenTTY()
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

func readPromptValue(prompt string, src io.Reader, stderr io.Writer, hidden bool) (string, error) {
	fmt.Fprint(stderr, prompt)

	if hidden {
		if f, ok := src.(*os.File); ok && output.IsTerminalReader(src) {
			value, err := term.ReadPassword(int(f.Fd()))
			fmt.Fprintln(stderr)
			return string(value), err
		}
	}

	value, err := readPromptLine(src)
	if err == nil {
		return strings.TrimRight(value, "\r\n"), nil
	}
	if err != io.EOF {
		return "", err
	}
	if value != "" {
		return strings.TrimRight(value, "\r\n"), nil
	}
	if hidden {
		return "", fmt.Errorf("unexpected EOF reading password")
	}
	return "", fmt.Errorf("unexpected EOF reading prompt")
}

func readPromptLine(src io.Reader) (string, error) {
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			b.WriteByte(buf[0])
			if buf[0] == '\n' {
				return b.String(), nil
			}
		}
		if err != nil {
			if err == io.EOF && b.Len() > 0 {
				return b.String(), io.EOF
			}
			return b.String(), err
		}
	}
}
