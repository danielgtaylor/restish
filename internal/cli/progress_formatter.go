package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/rest-sh/restish/v2/internal/output"
)

const defaultProgressFormatterTimeout = 5 * time.Second

type progressFormatterSession struct {
	formatter  output.ValueStreamFormatter
	stream     output.ValueStream
	ctx        context.Context
	cancel     context.CancelFunc
	subprocess bool
	timeout    time.Duration
}

func (c *CLI) newProgressFormatterSession(ctx context.Context, timeout time.Duration) *progressFormatterSession {
	formatter, ok := c.formatters["progress"].(output.ValueStreamFormatter)
	if !ok {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	subprocess := false
	if pluginFormatter, ok := formatter.(*output.PluginFormatter); ok {
		clone := *pluginFormatter
		clone.Context = ctx
		formatter = &clone
		subprocess = true
	}
	if timeout <= 0 {
		timeout = defaultProgressFormatterTimeout
	}
	return &progressFormatterSession{formatter: formatter, ctx: ctx, cancel: cancel, subprocess: subprocess, timeout: timeout}
}

func (s *progressFormatterSession) Write(w io.Writer, base *output.Response, color bool, value any) error {
	if s.stream == nil {
		var stream output.ValueStream
		err := s.run(func() error {
			var err error
			stream, err = s.formatter.StartValueStream(w, base, color)
			return err
		})
		if err != nil {
			if stream != nil {
				return errors.Join(err, stream.Close())
			}
			return err
		}
		s.stream = stream
	}
	stream := s.stream
	if err := s.run(func() error { return stream.WriteValue(value) }); err != nil {
		var closeErr error
		if s.ctx.Err() != nil {
			if s.subprocess {
				closeErr = stream.Close()
			}
			s.stream = nil
		} else {
			closeErr = s.closeStream()
		}
		return errors.Join(err, closeErr)
	}
	return nil
}

func (s *progressFormatterSession) Close() error {
	defer s.cancel()
	return s.closeStream()
}

func (s *progressFormatterSession) closeStream() error {
	if s.stream == nil {
		return nil
	}
	err := s.run(s.stream.Close)
	s.stream = nil
	return err
}

func (s *progressFormatterSession) run(fn func() error) error {
	if !s.subprocess {
		return fn()
	}
	done := make(chan error, 1)
	go func() { done <- fn() }()
	timer := time.NewTimer(s.timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-s.ctx.Done():
		s.cancel()
		return errors.Join(s.ctx.Err(), <-done)
	case <-timer.C:
		s.cancel()
		return errors.Join(fmt.Errorf("timed out after %s", s.timeout), <-done)
	}
}
