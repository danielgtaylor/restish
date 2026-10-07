package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unicode"

	"github.com/mattn/go-runewidth"
	"github.com/rest-sh/restish/v2/internal/output"
	pluginwire "github.com/rest-sh/restish/v2/plugin"
	"golang.org/x/term"
)

var errSelectCanceled = errors.New("selection canceled")

const selectWindowSize = 10

func (c *CLI) selectOne(ctx context.Context, message string, options []pluginwire.SelectOption) (value string, err error) {
	if len(options) == 0 {
		return "", fmt.Errorf("select requires at least one option")
	}
	labels := make([]string, len(options))
	for i, option := range options {
		if option.Label == "" {
			return "", fmt.Errorf("select option %d has an empty label", i+1)
		}
		labels[i] = selectLine(option.Label)
	}
	if len(options) == 1 {
		return options[0].Value, nil
	}
	src, cleanup, owned, err := c.selectSource()
	if err != nil {
		return "", err
	}
	defer cleanup()
	restore := func() error { return nil }
	if f, ok := src.(*os.File); ok && output.IsTerminalReader(f) {
		fd := int(f.Fd())
		state, rawErr := term.MakeRaw(fd)
		if rawErr != nil {
			return "", fmt.Errorf("select: enable raw terminal: %w", rawErr)
		}
		var once sync.Once
		var restoreErr error
		restore = func() error {
			once.Do(func() { restoreErr = term.Restore(fd, state) })
			return restoreErr
		}
		defer func() {
			if restoreErr := restore(); err == nil && restoreErr != nil {
				err = fmt.Errorf("select: restore terminal: %w", restoreErr)
			}
		}()
	}
	cancelWatchDone := make(chan struct{})
	if f, ok := src.(*os.File); ok && owned {
		go func() {
			select {
			case <-ctx.Done():
				_ = restore()
				_ = f.Close()
			case <-cancelWatchDone:
			}
		}()
	}
	defer close(cancelWatchDone)
	reader := newCancelableStdinReader(src, ctx.Done())
	width, height := 80, 24
	if f, ok := src.(*os.File); ok {
		if terminalWidth, terminalHeight, sizeErr := term.GetSize(int(f.Fd())); sizeErr == nil && terminalWidth > 0 && terminalHeight > 2 {
			width, height = terminalWidth, terminalHeight
		}
	}
	selected, drawn := 0, 0
	for {
		drawn, err = drawSelect(c.Stderr, message, labels, selected, drawn, width, height)
		if err != nil {
			return "", fmt.Errorf("select: render: %w", err)
		}
		key, readErr := readSelectKey(reader)
		if readErr != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return "", ctxErr
			}
			return "", fmt.Errorf("select: read input: %w", readErr)
		}
		switch key {
		case selectKeyUp:
			selected = (selected - 1 + len(options)) % len(options)
		case selectKeyDown:
			selected = (selected + 1) % len(options)
		case selectKeyEnter:
			return options[selected].Value, nil
		case selectKeyCancel:
			return "", errSelectCanceled
		}
	}
}

func (c *CLI) selectSource() (io.Reader, func(), bool, error) {
	if c.hooks.PassReader != nil {
		return c.hooks.PassReader, func() {}, false, nil
	}
	if !output.IsTerminal(c.Stderr) {
		return nil, nil, false, fmt.Errorf("select requires an interactive terminal")
	}
	if f, err := promptOpenTTY(); err == nil {
		if output.IsTerminalReader(f) {
			return f, func() { _ = f.Close() }, true, nil
		}
		_ = f.Close()
	}
	if output.IsTerminalReader(c.Stdin) {
		return c.Stdin, func() {}, false, nil
	}
	return nil, nil, false, fmt.Errorf("select requires an interactive terminal")
}

type selectKey byte

const (
	selectKeyNone selectKey = iota
	selectKeyUp
	selectKeyDown
	selectKeyEnter
	selectKeyCancel
)

func readSelectKey(r io.Reader) (selectKey, error) {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return selectKeyNone, err
	}
	switch b[0] {
	case '\r', '\n':
		return selectKeyEnter, nil
	case 3:
		return selectKeyCancel, nil
	case 0, 0xe0:
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return selectKeyNone, err
		}
		if b[0] == 72 {
			return selectKeyUp, nil
		}
		if b[0] == 80 {
			return selectKeyDown, nil
		}
	case 0x1b:
		var sequence [2]byte
		if _, err := io.ReadFull(r, sequence[:]); err != nil {
			return selectKeyNone, err
		}
		if sequence[0] == '[' || sequence[0] == 'O' {
			if sequence[1] == 'A' {
				return selectKeyUp, nil
			}
			if sequence[1] == 'B' {
				return selectKeyDown, nil
			}
		}
	}
	return selectKeyNone, nil
}

func drawSelect(w io.Writer, message string, labels []string, selected, previousLines, width, height int) (int, error) {
	visible := min(len(labels), selectWindowSize, max(1, height-2))
	start := max(0, selected-visible/2)
	start = min(start, len(labels)-visible)
	var screen strings.Builder
	if previousLines > 0 {
		fmt.Fprintf(&screen, "\x1b[%dA", previousLines)
	}
	message = selectLine(message)
	if message == "" {
		message = "Select"
	}
	suffix := fmt.Sprintf(" (%d/%d, arrow keys, Enter)", selected+1, len(labels))
	if width < runewidth.StringWidth(suffix)+8 {
		suffix = fmt.Sprintf(" (%d/%d)", selected+1, len(labels))
	}
	message = fitSelectLine(message, max(1, width-runewidth.StringWidth(suffix)))
	fmt.Fprintf(&screen, "\r\x1b[2K%s%s\n", message, suffix)
	for i := start; i < start+visible; i++ {
		prefix := "  "
		if i == selected {
			prefix = "> "
		}
		fmt.Fprintf(&screen, "\r\x1b[2K%s%s\n", prefix, fitSelectLine(labels[i], max(1, width-2)))
	}
	screen.WriteByte('\r')
	_, err := io.WriteString(w, screen.String())
	return visible + 1, err
}

func fitSelectLine(value string, width int) string {
	tail := ""
	if width > 3 && runewidth.StringWidth(value) > width {
		tail = "..."
	}
	return runewidth.Truncate(value, width, tail)
}

func selectLine(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsGraphic(r) {
			return r
		}
		return ' '
	}, value)
}
