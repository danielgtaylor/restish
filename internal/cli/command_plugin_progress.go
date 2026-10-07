package cli

import (
	"fmt"

	"github.com/rest-sh/restish/v2/internal/output"
	pluginwire "github.com/rest-sh/restish/v2/plugin"
	"github.com/spf13/cobra"
)

type commandProgressRenderer struct {
	session  *progressFormatterSession
	cmd      *cobra.Command
	disabled bool
	fallback []string
	bytes    int
}

func (c *CLI) newCommandProgressRenderer(cmd *cobra.Command) *commandProgressRenderer {
	session := c.newProgressFormatterSession(cmd.Context(), commandPluginShutdownGrace())
	if session == nil {
		return nil
	}
	return &commandProgressRenderer{session: session, cmd: cmd}
}

func (r *commandProgressRenderer) Write(msg pluginwire.ProgressMsg) (bool, error) {
	if r.disabled {
		return false, nil
	}
	if (msg.Current == nil) != (msg.Total == nil) || msg.Current != nil && (*msg.Current < 0 || *msg.Total <= 0 || *msg.Current > *msg.Total) {
		return false, nil
	}
	if msg.Text != "" && r.bytes+len(msg.Text)+1 > maxCommandPluginDataBytes {
		err := r.Close()
		r.disabled = true
		return false, err
	}
	record := map[string]any{
		"id":      msg.ID,
		"label":   msg.Label,
		"state":   msg.State,
		"current": msg.Current,
		"total":   msg.Total,
		"unit":    msg.Unit,
		"message": msg.Message,
	}
	// Command stderr is not coordinated with formatter redraws yet, so keep
	// this session line-oriented even when stderr is a terminal.
	if err := r.session.Write(r.cmd.ErrOrStderr(), &output.Response{}, false, record); err != nil {
		r.replayFallback()
		r.disabled = true
		return false, err
	}
	if msg.Text != "" {
		r.fallback = append(r.fallback, msg.Text)
		r.bytes += len(msg.Text) + 1
	}
	return true, nil
}

func (r *commandProgressRenderer) Close() error {
	err := r.session.Close()
	if err != nil {
		r.replayFallback()
	} else {
		r.fallback = nil
		r.bytes = 0
	}
	return err
}

func (r *commandProgressRenderer) replayFallback() {
	for _, text := range r.fallback {
		fmt.Fprintln(r.cmd.ErrOrStderr(), text)
	}
	r.fallback = nil
	r.bytes = 0
}
