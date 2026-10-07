package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rest-sh/restish/v2/internal/output"
)

const downloadProgressInterval = 100 * time.Millisecond

type downloadProgress struct {
	session  *progressFormatterSession
	ctx      context.Context
	stderr   io.Writer
	base     *output.Response
	total    int64
	current  int64
	last     time.Time
	now      func() time.Time
	color    bool
	disabled bool
	warned   bool
}

func (c *CLI) newDownloadProgress(ctx context.Context, resp *http.Response) *downloadProgress {
	if !c.stderrIsTerminal() || resp == nil || resp.Body == nil || resp.ContentLength == 0 || output.ResponseHasNoBody(resp) {
		return nil
	}
	session := c.newProgressFormatterSession(ctx, defaultProgressFormatterTimeout)
	if session == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p := &downloadProgress{
		session: session,
		ctx:     ctx,
		stderr:  c.Stderr,
		base:    responseMetadataOnly(resp),
		total:   resp.ContentLength,
		now:     time.Now,
		color:   output.ColorEnabled(c.Stderr),
	}
	p.last = p.now()
	if err := p.write("running"); err != nil {
		p.warn(err)
		_ = p.session.Close()
		return nil
	}
	return p
}

func (p *downloadProgress) observe(n int) {
	if p == nil || p.disabled || n <= 0 {
		return
	}
	p.current += int64(n)
	if p.total > 0 && p.current >= p.total {
		return
	}
	now := p.now()
	if now.Sub(p.last) < downloadProgressInterval {
		return
	}
	p.last = now
	if err := p.write("running"); err != nil {
		p.warn(err)
		p.disabled = true
		_ = p.session.Close()
	}
}

func (p *downloadProgress) finish(readErr error) {
	if p == nil || p.disabled {
		return
	}
	state := "success"
	if readErr != nil {
		state = "failed"
	}
	if err := p.write(state); err != nil {
		p.warn(err)
	}
	if err := p.session.Close(); err != nil {
		p.warn(err)
	}
	p.disabled = true
}

func (p *downloadProgress) write(state string) error {
	record := map[string]any{
		"id":    "http-download",
		"label": "Download",
		"state": state,
		"unit":  "bytes",
	}
	if p.total > 0 {
		record["current"] = p.current
		record["total"] = p.total
	} else {
		record["message"] = fmt.Sprintf("%d bytes", p.current)
	}
	return p.session.Write(p.stderr, p.base, p.color, record)
}

func (p *downloadProgress) warn(err error) {
	if p.warned || p.ctx.Err() != nil {
		return
	}
	p.warned = true
	writeDiagnostic(p.stderr, diagnosticWarn, "warning", "download progress unavailable: %v", err)
}

type downloadProgressReader struct {
	reader   io.Reader
	progress *downloadProgress
}

func (r downloadProgressReader) Read(buf []byte) (int, error) {
	n, err := r.reader.Read(buf)
	r.progress.observe(n)
	return n, err
}
