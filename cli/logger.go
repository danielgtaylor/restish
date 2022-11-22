package cli

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"github.com/alecthomas/chroma/quick"
)

var enableVerbose bool

// LogDebug logs a debug message if --rsh-verbose (-v) was passed.
func LogDebug(format string, values ...interface{}) {
	if enableVerbose {
		fmt.Fprintf(Stderr, "%s %s\n", au.Index(243, "DEBUG:"), fmt.Sprintf(format, values...))
	}
}

// LogDebugRequest logs the request in a debug message if verbose output
// is enabled.
func LogDebugRequest(req *http.Request) {
	if enableVerbose {
		dumped, err := httputil.DumpRequest(req, true)
		if err != nil {
			return
		}

		if useColor {
			sb := &strings.Builder{}
			quick.Highlight(sb, string(dumped), "http", "terminal256", "cli-dark")
			dumped = []byte(sb.String())
		}

		LogDebug("Making request:\n%s", string(dumped))
	}
}

// LogDebugResponse logs the response in a debug message if verbose output
// is enabled.
func LogDebugResponse(start time.Time, resp *http.Response) {
	if enableVerbose {
		dumped, err := httputil.DumpResponse(resp, true)
		if err != nil {
			return
		}

		if useColor {
			sb := &strings.Builder{}
			quick.Highlight(sb, string(dumped), "http", "terminal256", "cli-dark")
			dumped = []byte(sb.String())
		}

		LogDebug("Got response from server in %s:\n%s", time.Since(start), string(dumped))
	}
}

// LogInfo logs an info message.
func LogInfo(format string, values ...interface{}) {
	fmt.Fprintf(Stderr, "%s %s\n", au.Index(74, "INFO:"), fmt.Sprintf(format, values...))
}

// LogWarning logs a warning message.
func LogWarning(format string, values ...interface{}) {
	fmt.Fprintf(Stderr, "%s %s\n", au.Index(222, "WARN:"), fmt.Sprintf(format, values...))
}

// LogError logs an error message.
func LogError(format string, values ...interface{}) {
	// TODO: stack traces?
	fmt.Fprintf(Stderr, "%s %s\n", au.BgIndex(204, "ERROR:").White().Bold(), fmt.Sprintf(format, values...))
}
