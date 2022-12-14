package bulk

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/danielgtaylor/mexpr"
	"github.com/danielgtaylor/restish/cli"
	"github.com/danielgtaylor/restish/openapi"
	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
	"github.com/pb33f/libopenapi/datamodel/high/base"
	"github.com/pb33f/libopenapi/datamodel/low"
	lowbase "github.com/pb33f/libopenapi/datamodel/low/base"
	"github.com/pb33f/libopenapi/index"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

var afs afero.Fs = afero.NewOsFs()

// panicOnErr panics if an error is passed, otherwise does nothing.
func panicOnErr(err error) {
	if err != nil {
		panic(err)
	}
}

// isFalsey returns if a value is falsey, such as `0`, `""`, `[]any{}`, etc.
// Empty slices and maps are considered falsey.
func isFalsey(v any) bool {
	switch t := v.(type) {
	case bool:
		return !t
	case int, int8, int16, int32, uint, uint8, uint16, uint32, float32, float64:
		return t == 0
	case string:
		return len(t) == 0
	case []byte:
		return len(t) == 0
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	}
	return false
}

// newInterpreter creates a new mexpr interpreter, optionally with type
// checking if a JSON Schema is available to describe the structure of the
// input. Parse errors are logged as warnings since there could be false
// positives - the idea is to provide help to the user for debugging.
func newInterpreter(expression, schemaURL string) mexpr.Interpreter {
	var example map[string]any

	if schemaURL != "" {
		// We have a schema which might be a JSON Schema we can understand. Let's
		// try to download, parse, and generate an example for the type checker.
		// Note: JSON Schema supports a superset of the mexpr types, for example
		// one-ofs and if/then/else. Some schemas will result in warnings that
		// may be false positives, but this is still a useful feature worth
		// keeping in my opinion.
		req, _ := http.NewRequest(http.MethodGet, schemaURL, nil)
		if resp, err := cli.MakeRequest(req); err == nil && resp.StatusCode < 300 {
			cli.DecodeResponse(resp)
			defer resp.Body.Close()
			if body, err := io.ReadAll(resp.Body); err == nil {

				var rootNode yaml.Node
				var ls lowbase.Schema

				if err := yaml.Unmarshal(body, &rootNode); err == nil {
					if err := low.BuildModel(rootNode.Content[0], &ls); err == nil {
						if err := ls.Build(rootNode.Content[0], index.NewSpecIndex(&rootNode)); err == nil {
							s := base.NewSchema(&ls)
							result := openapi.GenExample(s, 0)
							if asMap, ok := result.(map[string]any); ok {
								example = asMap
							}
						}
					}
				}
			}
		}
	}

	ast, err := mexpr.Parse(expression, example, mexpr.UnquotedStrings)
	if err != nil {
		cli.LogWarning(err.Pretty(expression))
		// Just return a falsey value to filter these files out.
		ast = &mexpr.Node{
			Type:  mexpr.NodeLiteral,
			Value: 0,
		}
	}

	return mexpr.NewInterpreter(ast, mexpr.UnquotedStrings)
}

// collectFiles gets a list of files to manipulate for a given command, taking
// into account what was passed on the commandline, any filter matching options,
// and whether to include files which have been deleted on disk but are still
// present in the metadata index.
func collectFiles(meta *Meta, args []string, match string, includeDeleted bool) []string {
	if len(args) == 0 {
		// No files passed in, so let's find them!
		seen := map[string]bool{}
		afero.Walk(afs, ".", func(path string, f fs.FileInfo, err error) error {
			if f.IsDir() || strings.HasPrefix(path, ".") {
				return nil
			}

			args = append(args, path)
			seen[path] = true
			return nil
		})

		if includeDeleted {
			for _, f := range meta.Files {
				if !seen[f.Path] {
					args = append(args, f.Path)
				}
			}
		}
	}

	if match != "" {
		newArgs := []string{}

		interpreters := map[string]mexpr.Interpreter{}
		for _, path := range args {
			schema := ""
			if f := meta.Files[path]; f != nil {
				schema = f.Schema
			}

			// Individual resource types could have their own schemas. We build a
			// registry of one interpreter for each distinct type that has a schema.
			i := interpreters[schema]
			if i == nil {
				interpreters[schema] = newInterpreter(match, schema)
				i = interpreters[schema]
			}

			var v any
			b, _ := afero.ReadFile(afs, path)
			json.Unmarshal(b, &v)
			result, err := i.Run(v)
			if err != nil || result == nil || isFalsey(result) {
				// Skip!
				continue
			}
			newArgs = append(newArgs, path)
		}

		args = newArgs
	}

	sort.Strings(args)

	return args
}

// loadMeta loads the Restish bulk metadata file from disk if possible.
func loadMeta(meta *Meta) error {
	b, err := afero.ReadFile(afs, metaFile)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, meta)
}

// mustLoadMeta loads the metadata file or panics.
func mustLoadMeta() *Meta {
	var m Meta
	panicOnErr(loadMeta(&m))
	return &m
}

// getStatus displays the current status of the checkout, including both
// remote and local changes.
func getStatus() error {
	meta := mustLoadMeta()
	local, remote, err := meta.GetChanged(collectFiles(meta, []string{}, "", false))
	if err != nil {
		return err
	}

	if len(remote) > 0 {
		fmt.Fprintf(cli.Stdout, "Remote changes\n  (use \"%s bulk pull\" to update)\n", os.Args[0])
		for _, changed := range remote {
			fmt.Fprintln(cli.Stdout, changed)
		}
	} else {
		fmt.Fprintln(cli.Stdout, "You are up to date with "+meta.URL)
	}

	if len(local) == 0 {
		fmt.Fprintln(cli.Stdout, "No local changes")
		return nil
	}

	fmt.Fprintf(cli.Stdout, "Local changes:\n  (use \"%s bulk reset [file]...\" to undo)\n  (use \"%s bulk diff [file]...\" to view changes)\n", os.Args[0], os.Args[0])
	for _, changed := range local {
		fmt.Fprintln(cli.Stdout, changed)
	}

	return nil
}

// diff a single file
func diff(path, url string, original, modified []byte) {
	var parsedOrig, parsedMod any
	var err error

	if len(original) > 0 {
		json.Unmarshal(original, &parsedOrig)
		original, err = cli.MarshalShort("json", true, parsedOrig)
		panicOnErr(err)
	}

	if len(modified) > 0 {
		json.Unmarshal(modified, &parsedMod)
		modified, err = cli.MarshalShort("json", true, parsedMod)
		panicOnErr(err)
	}

	edits := myers.ComputeEdits(span.URIFromPath("remote"), string(original), string(modified))

	if len(edits) == 0 {
		fmt.Fprintln(cli.Stdout, "No changes made.")
		return
	} else {
		diff := fmt.Sprint(gotextdiff.ToUnified("remote "+url, "local "+path, string(original), edits))
		if viper.GetBool("color") {
			d, _ := cli.Highlight("diff", []byte(diff))
			diff = string(d)
		}
		fmt.Fprintln(cli.Stdout, diff)
	}
}

// getAllDiffs for the given set of file paths. Displays one diff per file
// without any separators.
func getAllDiffs(files []string) error {
	meta := mustLoadMeta()
	for _, path := range files {
		var orig, modified []byte
		if f, ok := meta.Files[path]; ok {
			if !f.IsChangedLocal(false) {
				continue
			}
			orig, _ = f.Fetch()
		}
		modified, _ = afero.ReadFile(afs, path)
		diff(path, meta.Base+strings.TrimSuffix(path, ".json"), orig, modified)
	}

	return nil
}

// Setup the bulk commands given a parent command.
func Setup(cmd *cobra.Command) {
	bulk := cobra.Command{
		GroupID: "generic",
		Use:     "bulk",
		Short:   "Client-side bulk resource management",
	}

	init := cobra.Command{
		Use:     "init URL [-f filter]",
		Aliases: []string{"i"},
		Short:   "Initialize a new bulk checkout. Start here.",
		Args:    cobra.ExactArgs(1),
		Example: "  " + os.Args[0] + " bulk init api.example.com/users -f 'body.{url, version: last_modified}'",
		Run: func(cmd *cobra.Command, args []string) {
			var m Meta
			loadMeta(&m)
			template, _ := cmd.Flags().GetString("url-template")
			panicOnErr(m.Init(args[0], template))
		},
	}
	init.Flags().String("url-template", "", "URL template to build links (e.g. from item IDs)")

	list := cobra.Command{
		Use:     "list [-m match]",
		Aliases: []string{"ls"},
		Short:   "List checked out files",
		Args:    cobra.NoArgs,
		Example: "  " + os.Args[0] + " bulk list -m 'id contains abc'\n  " + os.Args[0] + " bulk list -m 'reviews where rating > 4'",
		Run: func(cmd *cobra.Command, args []string) {
			match, _ := cmd.Flags().GetString("match")
			for _, path := range collectFiles(mustLoadMeta(), args, match, false) {
				fmt.Fprintln(cli.Stdout, path)
			}
		},
	}
	list.Flags().StringP("match", "m", "", "Expression to match")

	pull := cobra.Command{
		Use:     "pull",
		Aliases: []string{"pl"},
		Short:   "Pull remote updates. Does not overwrite local changes.",
		Args:    cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			panicOnErr(mustLoadMeta().Pull())
		},
	}

	status := cobra.Command{
		Use:     "status",
		Aliases: []string{"st"},
		Short:   "Show the local & remote added/changed/removed files",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return getStatus()
		},
	}

	diff := cobra.Command{
		Use:     "diff [file]...",
		Aliases: []string{"di"},
		Short:   "Show a diff of local changed files",
		Run: func(cmd *cobra.Command, args []string) {
			match, _ := cmd.Flags().GetString("match")
			panicOnErr(getAllDiffs(collectFiles(mustLoadMeta(), args, match, true)))
		},
	}
	diff.Flags().StringP("match", "m", "", "Expression to match")

	reset := cobra.Command{
		Use:     "reset [file]...",
		Aliases: []string{"re"},
		Short:   "Undo local changes to files",
		Run: func(cmd *cobra.Command, args []string) {
			meta := mustLoadMeta()
			match, _ := cmd.Flags().GetString("match")
			for _, name := range collectFiles(meta, args, match, true) {
				panicOnErr(meta.Files[name].Reset())
			}
			panicOnErr(meta.Save())
		},
	}
	reset.Flags().StringP("match", "m", "", "Expression to match")

	push := cobra.Command{
		Use:     "push",
		Aliases: []string{"ps"},
		Short:   "Upload local changes to the remote server",
		Args:    cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			// TODO: limit, pause-every, wait-between, etc to control uploads.
			panicOnErr(mustLoadMeta().Push())
		},
	}

	bulk.AddCommand(&init)
	bulk.AddCommand(&list)
	bulk.AddCommand(&pull)
	bulk.AddCommand(&status)
	bulk.AddCommand(&diff)
	bulk.AddCommand(&reset)
	bulk.AddCommand(&push)

	cmd.AddCommand(&bulk)
}
