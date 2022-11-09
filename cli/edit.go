package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/danielgtaylor/shorthand/v2"
	"github.com/google/shlex"
	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
	"github.com/mattn/go-isatty"
	"github.com/spf13/viper"
)

func panicOnErr(err error) {
	if err != nil {
		panic(err)
	}
}

// getEditor tries to find the system default text editor command.
func getEditor() string {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}

	return editor
}

func edit(addr string, args []string, interactive, noPrompt bool, exitFunc func(int), editMarshal func(interface{}) ([]byte, error), editUnmarshal func([]byte, interface{}) error, ext string) {
	if !interactive && len(args) == 0 {
		fmt.Fprintln(os.Stderr, "No arguments passed to modify the resource. Use `-i` to enable interactive mode.")
		exitFunc(1)
		return
	}

	editor := getEditor()
	if interactive && editor == "" {
		fmt.Fprintln(os.Stderr, `Please set the VISUAL or EDITOR environment variable with your preferred editor. Examples:

export VISUAL="code --wait"
export EDITOR="vim"`)
		exitFunc(1)
		return
	}

	req, _ := http.NewRequest(http.MethodGet, fixAddress(addr), nil)
	resp, err := GetParsedResponse(req)
	panicOnErr(err)

	if resp.Status >= 400 {
		panicOnErr(Formatter.Format(resp))
		exitFunc(1)
		return
	}

	// Convert from CBOR or other formats which might allow map[any]any to the
	// constraints of JSON (i.e. map[string]interface{}).
	var data interface{} = resp.Map()
	data = makeJSONSafe(data, false)

	filter := viper.GetString("rsh-filter")
	if filter == "" {
		filter = "body"
	}

	var logger func(format string, a ...interface{})
	if enableVerbose {
		logger = LogDebug
	}
	filtered, _, err := shorthand.GetPath(filter, data, shorthand.GetOptions{
		DebugLogger: logger,
	})
	panicOnErr(err)
	data = filtered

	if _, ok := data.(map[string]interface{}); !ok {
		fmt.Fprintln(os.Stderr, "Resource didn't return an object.")
		exitFunc(1)
		return
	}

	// Save original representation for comparison later. We use JSON here for
	// consistency and to avoid things like YAML encoding e.g. dates and strings
	// differently.
	orig, _ := json.MarshalIndent(data, "", "  ")

	// If available, grab any headers that can be used for conditional updates
	// so we don't overwrite changes made by other people while we edit.
	etag := resp.Headers["Etag"]
	lastModified := resp.Headers["Last-Modified"]

	// TODO: remove read-only fields? This requires:
	// 1. Figure out which operation the URL corresponds to.
	// 2. Get and then analyse the response schema for that operation.
	// 3. Remove corresponding fields from `data`.

	var modified interface{} = data

	if len(args) > 0 {
		modified, err = shorthand.Unmarshal(strings.Join(args, " "), shorthand.ParseOptions{EnableFileInput: true, EnableObjectDetection: true}, modified)
		panicOnErr(err)
	}

	if interactive {
		// Create temp file
		tmp, err := os.CreateTemp("", "rsh-edit*"+ext)
		panicOnErr(err)
		defer os.Remove(tmp.Name())

		// TODO: should we try and detect a `describedby` link relation and insert
		// that as a `$schema` key into the document before editing? The schema
		// itself may not allow the `$schema` key... hmm.

		// Write the current body
		marshalled, err := editMarshal(modified)
		panicOnErr(err)
		tmp.Write(marshalled)
		tmp.Close()

		// Open editor and wait for exit
		parts, err := shlex.Split(editor)
		panicOnErr(err)
		name := parts[0]
		args := append(parts[1:], tmp.Name())

		cmd := exec.Command(name, args...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		panicOnErr(cmd.Run())

		// Read file contents
		b, err := os.ReadFile(tmp.Name())
		panicOnErr(err)

		panicOnErr(editUnmarshal(b, &modified))
	}

	modified = makeJSONSafe(modified, false)
	mod, err := json.MarshalIndent(modified, "", "  ")
	panicOnErr(err)
	edits := myers.ComputeEdits(span.URIFromPath("original"), string(orig), string(mod))

	if len(edits) == 0 {
		fmt.Fprintln(os.Stderr, "No changes made.")
		exitFunc(0)
		return
	} else {
		diff := fmt.Sprint(gotextdiff.ToUnified("original", "modified", string(orig), edits))
		if tty {
			d, _ := Highlight("diff", []byte(diff))
			diff = string(d)
		}
		fmt.Println(diff)

		if !noPrompt && isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd()) {
			fmt.Printf("Continue? [Y/n] ")
			tmp := []byte{0}
			os.Stdin.Read(tmp)
			if tmp[0] == 'n' {
				exitFunc(0)
				return
			}
		}
	}

	// TODO: support different submission formats, e.g. based on any given
	// `Content-Type` header?
	// TODO: content-encoding for large bodies?
	// TODO: determine if a PATCH could be used instead?
	b, _ := json.Marshal(modified)
	req, _ = http.NewRequest(http.MethodPut, fixAddress(addr), bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")

	if etag != "" {
		req.Header.Set("If-Match", etag)
	} else if lastModified != "" {
		req.Header.Set("If-Unmodified-Since", lastModified)
	}

	MakeRequestAndFormat(req)
}
