package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rest-sh/restish/v2/internal/cli"
	pluginwire "github.com/rest-sh/restish/v2/plugin"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type target struct {
	path    string
	regions []string
}

var targets = []target{
	{path: "site/content/en/docs/reference/global-flags.md", regions: []string{"global-flags"}},
	{path: "site/content/en/docs/reference/http-commands.md", regions: []string{"http-commands"}},
	{path: "site/content/en/docs/reference/api-management.md", regions: []string{"api-command"}},
	{path: "site/content/en/docs/reference/config.md", regions: []string{"config-schema"}},
	{path: "site/content/en/docs/reference/profiles.md", regions: []string{"profile-schema"}},
	{path: "site/content/en/docs/reference/environment-variables.md", regions: []string{"environment-variables"}},
	{path: "site/content/en/docs/reference/config-command.md", regions: []string{"config-command"}},
	{path: "site/content/en/docs/reference/cache-command.md", regions: []string{"cache-command"}},
	{path: "site/content/en/docs/reference/doctor-command.md", regions: []string{"doctor-command"}},
	{path: "site/content/en/docs/reference/shell-command.md", regions: []string{"shell-command"}},
	{path: "site/content/en/docs/reference/utility-commands.md", regions: []string{"utility-commands"}},
	{path: "site/content/en/docs/reference/plugin-command.md", regions: []string{"plugin-command"}},
	{path: "site/content/en/docs/reference/edit-command.md", regions: []string{"edit-command"}},
	{path: "site/content/en/docs/reference/bulk-command.md", regions: []string{"bulk-help"}},
	{path: "site/content/en/docs/plugins/mcp.md", regions: []string{"mcp-help"}},
	{path: "site/content/en/docs/reference/plugin-manifest.md", regions: []string{"plugin-manifest-schema"}},
	{path: "site/content/en/docs/reference/plugin-messages.md", regions: []string{"plugin-message-schema"}},
}

type pluginBuild struct {
	name string
	pkg  string
}

var commandPlugins = []pluginBuild{
	{name: "bulk", pkg: "./cmd/restish-bulk"},
	{name: "mcp", pkg: "./cmd/restish-mcp"},
}

func main() {
	write := flag.Bool("write", false, "update generated regions in-place")
	check := flag.Bool("check", false, "fail if generated regions are stale")
	rootFlag := flag.String("repo-root", "", "repository root (default: discover from current directory)")
	flag.Parse()

	if *write == *check {
		fmt.Fprintln(os.Stderr, "usage: restish-docgen --write|--check [--repo-root PATH]")
		os.Exit(2)
	}

	if err := run(*rootFlag, *write); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(rootFlag string, write bool) error {
	root, err := repoRoot(rootFlag)
	if err != nil {
		return err
	}

	tempDir, err := os.MkdirTemp("", "restish-docgen-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	pluginDir := filepath.Join(tempDir, "plugins")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		return err
	}
	pluginPaths, err := buildCommandPlugins(root, pluginDir, filepath.Join(tempDir, "gocache"))
	if err != nil {
		return err
	}

	c := cli.New()
	c.SetSignalHandling(false)
	cmdRoot, err := c.RootCommandForDocs(cli.DocsCommandOptions{
		PluginDir:               pluginDir,
		PluginManifestCachePath: filepath.Join(tempDir, "plugin-manifest-cache.cbor"),
	})
	if err != nil {
		return err
	}

	regions, err := renderRegions(root, cmdRoot, pluginPaths)
	if err != nil {
		return err
	}

	var stale []string
	for _, target := range targets {
		path := filepath.Join(root, filepath.FromSlash(target.path))
		oldData, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		next := string(oldData)
		for _, region := range target.regions {
			rendered, ok := regions[region]
			if !ok {
				return fmt.Errorf("no renderer for generated region %q", region)
			}
			var replaceErr error
			next, replaceErr = replaceRegion(next, region, rendered)
			if replaceErr != nil {
				return fmt.Errorf("%s: %w", target.path, replaceErr)
			}
		}
		if next == string(oldData) {
			continue
		}
		if write {
			if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
				return err
			}
			continue
		}
		stale = append(stale, target.path)
	}

	if !write && len(stale) > 0 {
		sort.Strings(stale)
		return fmt.Errorf("generated docs are stale; run `go run ./cmd/restish-docgen --write`\n%s", strings.Join(stale, "\n"))
	}
	return nil
}

func repoRoot(rootFlag string) (string, error) {
	if rootFlag != "" {
		return filepath.Abs(rootFlag)
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "site", "content", "en", "docs")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("cannot find repository root")
		}
		dir = parent
	}
}

func buildCommandPlugins(root, pluginDir, goCache string) (map[string]string, error) {
	paths := make(map[string]string, len(commandPlugins))
	for _, plugin := range commandPlugins {
		out := filepath.Join(pluginDir, "restish-"+plugin.name)
		cmd := exec.Command("go", "build", "-o", out, plugin.pkg)
		cmd.Dir = root
		cmd.Env = os.Environ()
		if os.Getenv("GOCACHE") == "" {
			cmd.Env = append(cmd.Env, "GOCACHE="+goCache)
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("build %s: %w\n%s", plugin.pkg, err, strings.TrimSpace(stderr.String()))
		}
		paths[plugin.name] = out
	}
	return paths, nil
}

func renderRegions(root string, cmdRoot *cobra.Command, pluginPaths map[string]string) (map[string]string, error) {
	manifestSchema, err := renderSchemaRegion(root, []string{
		"Manifest",
		"CommandDecl",
		"CommandDiscoveryResponse",
	})
	if err != nil {
		return nil, err
	}
	messageSchema, err := renderMessageSchemaRegion(root)
	if err != nil {
		return nil, err
	}
	configSchema, err := renderConfigSchemaRegion(root)
	if err != nil {
		return nil, err
	}
	profileSchema, err := renderProfileSchemaRegion(root)
	if err != nil {
		return nil, err
	}
	envVars, err := renderEnvironmentVariablesRegion(root)
	if err != nil {
		return nil, err
	}
	bulkHelp, err := renderPluginHelpRegion(pluginPaths["bulk"], "bulk", [][]string{
		{"--help"},
		{"init", "--help"},
		{"list", "--help"},
		{"status", "--help"},
		{"diff", "--help"},
		{"pull", "--help"},
		{"push", "--help"},
		{"reset", "--help"},
	})
	if err != nil {
		return nil, err
	}
	mcpHelp, err := renderPluginHelpRegion(pluginPaths["mcp"], "mcp", [][]string{
		{"--help"},
		{"serve", "--help"},
	})
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"global-flags":           renderGlobalFlags(cmdRoot),
		"http-commands":          renderCommandDetails(cmdRoot, []string{"restish get", "restish head", "restish options", "restish post", "restish put", "restish patch", "restish delete"}),
		"api-command":            renderCommandDetails(cmdRoot, []string{"restish api", "restish api connect", "restish api sync", "restish api list", "restish api inspect", "restish api set", "restish api remove", "restish api auth", "restish api auth add", "restish api auth remove", "restish api auth logout", "restish api auth get", "restish api auth inspect"}),
		"config-command":         renderCommandDetails(cmdRoot, []string{"restish config", "restish config path", "restish config show", "restish config edit", "restish config set", "restish config theme", "restish config theme list", "restish config theme set", "restish config theme reset"}),
		"cache-command":          renderCommandDetails(cmdRoot, []string{"restish cache", "restish cache info", "restish cache clear"}),
		"doctor-command":         renderCommandDetails(cmdRoot, []string{"restish doctor", "restish doctor api", "restish doctor plugin"}),
		"shell-command":          renderCommandDetails(cmdRoot, []string{"restish shell", "restish shell setup", "restish shell completion", "restish shell completion bash", "restish shell completion zsh", "restish shell completion fish", "restish shell completion powershell", "restish shell completion install"}),
		"utility-commands":       renderCommandDetails(cmdRoot, []string{"restish cert", "restish links", "restish version"}),
		"plugin-command":         renderCommandDetails(cmdRoot, []string{"restish plugin", "restish plugin list", "restish plugin install", "restish plugin remove", "restish plugin debug"}),
		"edit-command":           renderCommandDetails(cmdRoot, []string{"restish edit"}),
		"bulk-help":              bulkHelp,
		"mcp-help":               mcpHelp,
		"plugin-manifest-schema": manifestSchema,
		"plugin-message-schema":  messageSchema,
		"config-schema":          configSchema,
		"profile-schema":         profileSchema,
		"environment-variables":  envVars,
	}, nil
}

func replaceRegion(content, name, generated string) (string, error) {
	start := "<!-- BEGIN GENERATED: restish-docgen " + name + " -->"
	end := "<!-- END GENERATED -->"
	startIdx := strings.Index(content, start)
	if startIdx < 0 {
		return "", fmt.Errorf("missing start marker for %q", name)
	}
	afterStart := startIdx + len(start)
	endIdxRel := strings.Index(content[afterStart:], end)
	if endIdxRel < 0 {
		return "", fmt.Errorf("missing end marker for %q", name)
	}
	endIdx := afterStart + endIdxRel + len(end)
	replacement := start + "\n" + strings.TrimRight(generated, "\n") + "\n" + end
	return content[:startIdx] + replacement + content[endIdx:], nil
}

func renderCommandIndex(root *cobra.Command) string {
	var out strings.Builder
	out.WriteString("Generated from the current Cobra command tree.\n\n")

	groups := map[string][]*cobra.Command{}
	for _, cmd := range sortedCommands(root) {
		if !cmd.IsAvailableCommand() && cmd.Name() != "help" {
			continue
		}
		group := commandIndexGroup(root, cmd)
		groups[group] = append(groups[group], cmd)
	}

	order := commandIndexGroupOrder(root, groups)
	for _, group := range order {
		cmds := groups[group]
		if len(cmds) == 0 {
			continue
		}
		out.WriteString("### ")
		out.WriteString(group)
		out.WriteString("\n\n")
		for _, cmd := range cmds {
			out.WriteString("**`")
			out.WriteString(cmd.CommandPath())
			out.WriteString("`**\n\n")
			if cmd.Short != "" {
				out.WriteString(mdParagraph(cmd.Short))
				out.WriteString("\n\n")
			}
			out.WriteString("Usage: `")
			out.WriteString(mdCode(cmd.UseLine()))
			out.WriteString("`\n\n")
		}
	}
	return out.String()
}

func commandIndexGroup(root, cmd *cobra.Command) string {
	if cmd == root {
		return "Root"
	}
	for current := cmd; current != nil && current != root; current = current.Parent() {
		if title := groupTitle(root, current.GroupID); title != "" {
			return title
		}
	}
	return "Additional Commands"
}

func commandIndexGroupOrder(root *cobra.Command, groups map[string][]*cobra.Command) []string {
	seen := map[string]bool{}
	var order []string
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		if _, ok := groups[name]; !ok {
			return
		}
		seen[name] = true
		order = append(order, name)
	}

	add("Root")
	for _, group := range root.Groups() {
		add(group.Title)
	}
	add("Additional Commands")
	for name := range groups {
		add(name)
	}
	return order
}

func sortedCommands(root *cobra.Command) []*cobra.Command {
	var cmds []*cobra.Command
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Hidden {
			return
		}
		cmds = append(cmds, cmd)
		children := append([]*cobra.Command(nil), cmd.Commands()...)
		sort.Slice(children, func(i, j int) bool {
			return children[i].CommandPath() < children[j].CommandPath()
		})
		for _, child := range children {
			walk(child)
		}
	}
	walk(root)
	return cmds
}

func groupTitle(root *cobra.Command, id string) string {
	if id == "" {
		return ""
	}
	for _, group := range root.Groups() {
		if group.ID == id {
			return group.Title
		}
	}
	return id
}

func renderGlobalFlags(root *cobra.Command) string {
	var out strings.Builder
	out.WriteString("Generated from the current root persistent flags.\n\n")
	out.WriteString(renderFlagList(root.PersistentFlags()))
	return out.String()
}

func renderCommandDetails(root *cobra.Command, paths []string) string {
	var out strings.Builder
	out.WriteString("Generated from the current Cobra command tree.\n")
	for _, path := range paths {
		cmd := findCommand(root, path)
		if cmd == nil {
			out.WriteString(fmt.Sprintf("\n### `%s`\n\nCommand not found in the current binary.\n", path))
			continue
		}
		out.WriteString(renderOneCommand(cmd))
	}
	return out.String()
}

func renderOneCommand(cmd *cobra.Command) string {
	var out strings.Builder
	out.WriteString(fmt.Sprintf("\n### `%s`\n\n", cmd.CommandPath()))
	if cmd.Short != "" {
		out.WriteString(mdParagraph(cmd.Short))
		out.WriteString("\n\n")
	}
	if long := strings.TrimSpace(commandLong(cmd)); long != "" {
		out.WriteString(long)
		out.WriteString("\n\n")
	}
	out.WriteString("Usage:\n\n")
	out.WriteString("```text\n")
	out.WriteString(cmd.UseLine())
	out.WriteString("\n```\n\n")
	if len(cmd.Aliases) > 0 {
		out.WriteString("Aliases: ")
		for i, alias := range cmd.Aliases {
			if i > 0 {
				out.WriteString(", ")
			}
			out.WriteString("`")
			out.WriteString(alias)
			out.WriteString("`")
		}
		out.WriteString("\n\n")
	}
	if example := strings.TrimRight(cmd.Example, "\n"); example != "" {
		out.WriteString("Examples:\n\n")
		out.WriteString("```bash\n")
		out.WriteString(strings.TrimLeft(example, "\n"))
		out.WriteString("\n```\n\n")
	}
	if subs := availableSubcommands(cmd); len(subs) > 0 {
		out.WriteString("Subcommands:\n\n")
		for _, sub := range subs {
			out.WriteString("**`")
			out.WriteString(sub.CommandPath())
			out.WriteString("`**")
			if sub.Short != "" {
				out.WriteString(": ")
				out.WriteString(mdParagraph(sub.Short))
			}
			out.WriteString("\n\n")
		}
	}
	if cmd.LocalFlags().HasAvailableFlags() {
		out.WriteString("Flags:\n\n")
		out.WriteString(renderFlagList(cmd.LocalFlags()))
		out.WriteString("\n")
	}
	return out.String()
}

func commandLong(cmd *cobra.Command) string {
	if cmd == nil {
		return ""
	}
	return cmd.Long
}

func availableSubcommands(cmd *cobra.Command) []*cobra.Command {
	subs := append([]*cobra.Command(nil), cmd.Commands()...)
	sort.Slice(subs, func(i, j int) bool {
		return subs[i].Name() < subs[j].Name()
	})
	out := subs[:0]
	for _, sub := range subs {
		if sub.IsAvailableCommand() || sub.Name() == "help" {
			out = append(out, sub)
		}
	}
	return out
}

func renderFlagList(flags *pflag.FlagSet) string {
	rows := flagRows(flags)
	if len(rows) == 0 {
		return "_None._\n"
	}
	var out strings.Builder
	for _, row := range rows {
		out.WriteString("**")
		out.WriteString(row.names)
		out.WriteString("**\n\n")
		out.WriteString("Type: `")
		out.WriteString(mdCode(row.typ))
		out.WriteString("`; default: ")
		out.WriteString(row.def)
		out.WriteString("\n\n")
		if row.usage != "" {
			out.WriteString(mdParagraph(row.usage))
			out.WriteString("\n\n")
		}
	}
	return out.String()
}

type flagRow struct {
	names string
	typ   string
	def   string
	usage string
}

func flagRows(flags *pflag.FlagSet) []flagRow {
	var rows []flagRow
	flags.VisitAll(func(flag *pflag.Flag) {
		if flag.Hidden {
			return
		}
		names := "`--" + flag.Name + "`"
		if flag.Shorthand != "" {
			names = "`-" + flag.Shorthand + "`, " + names
		}
		rows = append(rows, flagRow{
			names: names,
			typ:   flag.Value.Type(),
			def:   mdDefault(flag.DefValue),
			usage: flag.Usage,
		})
	})
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].names < rows[j].names
	})
	return rows
}

func mdDefault(value string) string {
	switch value {
	case "", "[]":
		return "none"
	default:
		return "`" + mdCode(value) + "`"
	}
}

func findCommand(root *cobra.Command, path string) *cobra.Command {
	parts := strings.Fields(path)
	if len(parts) == 0 {
		return nil
	}
	cmd := root
	if parts[0] == root.Name() {
		parts = parts[1:]
	}
	for _, part := range parts {
		var next *cobra.Command
		for _, child := range cmd.Commands() {
			if child.Name() == part || contains(child.Aliases, part) {
				next = child
				break
			}
		}
		if next == nil {
			return nil
		}
		cmd = next
	}
	return cmd
}

func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func renderPluginHelpRegion(path, command string, argSets [][]string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("plugin %s was not built", command)
	}
	var out strings.Builder
	out.WriteString("Generated from the compiled `restish-")
	out.WriteString(command)
	out.WriteString("` plugin binary.\n")
	for _, args := range argSets {
		help, err := commandPluginHelp(path, command, args)
		if err != nil {
			return "", err
		}
		out.WriteString("\n### `restish ")
		out.WriteString(command)
		if len(args) > 0 {
			out.WriteString(" ")
			out.WriteString(strings.Join(args, " "))
		}
		out.WriteString("`\n\n")
		out.WriteString("```text\n")
		out.WriteString(strings.TrimRight(help, "\n"))
		out.WriteString("\n```\n")
	}
	return out.String(), nil
}

func commandPluginHelp(path, command string, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	proc := exec.CommandContext(ctx, path)
	stdin, err := proc.StdinPipe()
	if err != nil {
		return "", err
	}
	stdout, err := proc.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr bytes.Buffer
	proc.Stderr = &stderr
	if err := proc.Start(); err != nil {
		return "", err
	}
	if err := pluginwire.WriteMessage(stdin, pluginwire.InitMsg{
		Type:    pluginwire.MsgTypeInit,
		Command: command,
		Args:    args,
	}); err != nil {
		_ = proc.Process.Kill()
		return "", err
	}
	_ = stdin.Close()

	var help bytes.Buffer
	dec := pluginwire.NewDecoder(stdout)
	for {
		raw, err := dec.ReadRaw()
		if err != nil {
			if ctx.Err() != nil {
				_ = proc.Process.Kill()
				return "", fmt.Errorf("plugin %s help timed out", command)
			}
			_ = proc.Process.Kill()
			return "", err
		}
		switch pluginwire.MessageType(raw) {
		case pluginwire.MsgTypeStdoutData:
			var msg pluginwire.StdoutDataMsg
			if err := pluginwire.DecMode.Unmarshal(raw, &msg); err != nil {
				_ = proc.Process.Kill()
				return "", err
			}
			help.Write(msg.Data)
		case pluginwire.MsgTypeDone:
			var msg pluginwire.DoneMsg
			if err := pluginwire.DecMode.Unmarshal(raw, &msg); err != nil {
				_ = proc.Process.Kill()
				return "", err
			}
			if err := proc.Wait(); err != nil {
				return "", fmt.Errorf("plugin %s help: %w: %s", command, err, strings.TrimSpace(stderr.String()))
			}
			if msg.ExitCode != 0 {
				return "", fmt.Errorf("plugin %s help exited %d", command, msg.ExitCode)
			}
			return help.String(), nil
		case pluginwire.MsgTypeStderrData, pluginwire.MsgTypeWarn, pluginwire.MsgTypeProgress, pluginwire.MsgTypeLog, pluginwire.MsgTypeSpinner:
			continue
		default:
			_ = proc.Process.Kill()
			return "", fmt.Errorf("plugin %s help emitted unexpected message type %q", command, pluginwire.MessageType(raw))
		}
	}
}

type fieldDoc struct {
	Name        string
	CBOR        string
	JSON        string
	Type        string
	Required    bool
	Description string
}

type structDoc struct {
	Name        string
	Description string
	Fields      []fieldDoc
}

func renderSchemaRegion(root string, names []string) (string, error) {
	structs, _, err := loadPluginDocs(root)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	out.WriteString("Generated from `plugin/manifest.go` and `plugin/messages.go`.\n")
	for _, name := range names {
		doc, ok := structs[name]
		if !ok {
			return "", fmt.Errorf("missing schema struct %s", name)
		}
		renderStructDoc(&out, doc)
	}
	return out.String(), nil
}

func renderMessageSchemaRegion(root string) (string, error) {
	structs, constants, err := loadPluginDocs(root)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	out.WriteString("Generated from `plugin/messages.go`.\n\n")
	out.WriteString("### Message Type Constants\n\n")
	out.WriteString("| Constant | Value |\n")
	out.WriteString("| --- | --- |\n")
	for _, c := range constants {
		out.WriteString(fmt.Sprintf("| `%s` | `%s` |\n", c.Name, c.Value))
	}
	for _, group := range [][]string{
		{"InitMsg", "HTTPRequestMsg", "HTTPResponseMsg", "APISpecMsg", "APISpecResponseMsg", "APIOperation", "APIParam", "ListAPIsMsg", "ListAPIsResponseMsg", "ListProfilesMsg", "ListProfilesResponseMsg", "ConfigReadMsg", "ConfigReadResponseMsg", "PromptMsg", "PromptResponseMsg", "ConfirmMsg", "ConfirmResponseMsg", "ResponseMsg", "DoneMsg", "StdoutDataMsg", "StderrDataMsg", "WarnMsg", "ProgressMsg", "SpinnerMsg", "LogMsg", "StdinDataMsg", "StdinCloseMsg"},
		{"FormatterResponse", "FormatterRequest", "FormatterOutput", "LoaderRequest", "LoaderResponse"},
		{"HookRequest", "HookRequestHeaderUpdate", "AuthHookInput", "AuthHookOutput", "RequestMiddlewareInput", "RequestMiddlewareOutput", "HookResponse", "ResponseMiddlewareInput", "FollowRequest", "HookResponseUpdate", "ResponseMiddlewareOutput"},
		{"TLSSignerInitMsg", "TLSSignerReadyMsg", "TLSSignerSignMsg", "TLSSignerSignedMsg", "TLSSignerShutdownMsg"},
	} {
		for _, name := range group {
			doc, ok := structs[name]
			if !ok {
				return "", fmt.Errorf("missing schema struct %s", name)
			}
			renderStructDoc(&out, doc)
		}
	}
	return out.String(), nil
}

func renderConfigSchemaRegion(root string) (string, error) {
	structs, err := loadStructDocs(root, []string{"config/config.go"})
	if err != nil {
		return "", err
	}
	return renderJSONStructTables("Generated from `config/config.go`.", structs, []string{
		"Config",
		"APIConfig",
		"PaginationConfig",
		"CacheConfig",
		"AuthConfig",
	}), nil
}

func renderProfileSchemaRegion(root string) (string, error) {
	structs, err := loadStructDocs(root, []string{"config/config.go"})
	if err != nil {
		return "", err
	}
	return renderJSONStructTables("Generated from `config/config.go`.", structs, []string{
		"ProfileConfig",
		"CredentialConfig",
		"AuthConfig",
	}), nil
}

func renderJSONStructTables(intro string, structs map[string]structDoc, names []string) string {
	var out strings.Builder
	out.WriteString(intro)
	out.WriteString("\n")
	for _, name := range names {
		doc, ok := structs[name]
		if !ok {
			out.WriteString("\n### `")
			out.WriteString(name)
			out.WriteString("`\n\n_Missing from source._\n")
			continue
		}
		out.WriteString("\n### `")
		out.WriteString(doc.Name)
		out.WriteString("`\n\n")
		if doc.Description != "" {
			out.WriteString(mdParagraph(doc.Description))
			out.WriteString("\n\n")
		}
		out.WriteString("| JSON field | Go field | Type | Required | Description |\n")
		out.WriteString("| --- | --- | --- | --- | --- |\n")
		for _, field := range doc.Fields {
			if field.JSON == "" {
				continue
			}
			out.WriteString("| `")
			out.WriteString(mdCode(field.JSON))
			out.WriteString("` | `")
			out.WriteString(mdCode(field.Name))
			out.WriteString("` | `")
			out.WriteString(mdCode(field.Type))
			out.WriteString("` | ")
			out.WriteString(boolWord(field.Required))
			out.WriteString(" | ")
			out.WriteString(md(field.Description))
			out.WriteString(" |\n")
		}
	}
	return out.String()
}

type envDoc struct {
	Name        string
	Group       string
	Description string
	Source      string
}

var envDocs = []envDoc{
	{Name: "RSH_CONFIG", Group: "Config And Profiles", Description: "Explicit config file path. It selects one config file for the invocation.", Source: "config paths"},
	{Name: "RSH_CONFIG_DIR", Group: "Config And Profiles", Description: "Config directory override; Restish uses `restish.json` inside this directory.", Source: "config paths"},
	{Name: "RSH_CACHE_DIR", Group: "Config And Profiles", Description: "HTTP/spec cache directory override.", Source: "config paths"},
	{Name: "XDG_CONFIG_HOME", Group: "Config And Profiles", Description: "Base config directory; Restish uses `$XDG_CONFIG_HOME/restish/restish.json`.", Source: "config paths"},
	{Name: "XDG_CACHE_HOME", Group: "Config And Profiles", Description: "Base cache directory; Restish uses `$XDG_CACHE_HOME/restish`.", Source: "config paths"},
	{Name: "RSH_PROFILE", Group: "Config And Profiles", Description: "Default API profile name. The `--rsh-profile` flag wins for one command.", Source: "global flags"},
	{Name: "RSH_AUTH", Group: "Config And Profiles", Description: "Default generated-operation auth override, such as `PartnerKey` or `UserOAuth+PartnerKey`.", Source: "global flags"},
	{Name: "RSH_HEADER", Group: "Request Defaults", Description: "Comma-separated default request headers in `Name: Value` form; escape literal commas as `\\,`.", Source: "global flags"},
	{Name: "RSH_QUERY", Group: "Request Defaults", Description: "Comma-separated default query parameters in `key=value` form; escape literal commas as `\\,`.", Source: "global flags"},
	{Name: "RSH_TIMEOUT", Group: "Request Defaults", Description: "Default request timeout such as `15s`.", Source: "global flags"},
	{Name: "RSH_FILTER", Group: "Request Defaults", Description: "Default response filter expression.", Source: "global flags"},
	{Name: "RSH_NO_CACHE", Group: "Request Defaults", Description: "Bypass HTTP cache where supported.", Source: "global flags"},
	{Name: "RSH_INSECURE", Group: "Request Defaults", Description: "Disable TLS certificate verification when truthy.", Source: "global flags"},
	{Name: "RSH_RETRY", Group: "Request Defaults", Description: "Default retry count where supported.", Source: "global flags"},
	{Name: "RSH_RETRY_UNSAFE", Group: "Request Defaults", Description: "Allow retry replay for POST, PUT, PATCH, and DELETE when truthy.", Source: "global flags"},
	{Name: "RSH_RETRY_MAX_WAIT", Group: "Request Defaults", Description: "Default cap for `Retry-After` / `X-Retry-In`, such as `30s`.", Source: "global flags"},
	{Name: "RSH_OUTPUT_FORMAT", Group: "Editor And Terminal", Description: "Default rendered body format for `-o` / `--rsh-output-format`.", Source: "global flags"},
	{Name: "RSH_PRINT", Group: "Editor And Terminal", Description: "Default `--rsh-print` output parts, such as `b` for compact rendered output in scripts.", Source: "global flags"},
	{Name: "VISUAL", Group: "Editor And Terminal", Description: "Preferred editor for `config edit` and `edit`.", Source: "editor"},
	{Name: "EDITOR", Group: "Editor And Terminal", Description: "Fallback editor for `config edit` and `edit`.", Source: "editor"},
	{Name: "BROWSER", Group: "Editor And Terminal", Description: "Browser command used for OAuth authorization-code flows (may include arguments). When set, Restish invokes it directly instead of `xdg-open` (Linux), `open` (macOS), or `rundll32 url.dll,FileProtocolHandler` (Windows).", Source: "oauth browser"},
	{Name: "GLAMOUR_STYLE", Group: "Editor And Terminal", Description: "Markdown rendering style for markdown-formatted terminal output.", Source: "output"},
	{Name: "RSH_IMAGE_PROTOCOL", Group: "Editor And Terminal", Description: "Force terminal image rendering protocol: `kitty`, `iterm2`, or `halfblock`.", Source: "output"},
	{Name: "KITTY_WINDOW_ID", Group: "Editor And Terminal", Description: "Used to auto-detect Kitty image support.", Source: "output"},
	{Name: "TERM", Group: "Editor And Terminal", Description: "Used to auto-detect Kitty terminal support.", Source: "output"},
	{Name: "TERM_PROGRAM", Group: "Editor And Terminal", Description: "Used to auto-detect iTerm2-style image support.", Source: "output"},
	{Name: "COLUMNS", Group: "Editor And Terminal", Description: "Terminal width hint for half-block image rendering.", Source: "output"},
	{Name: "SHELL", Group: "Editor And Terminal", Description: "Used for first-run shell setup hints.", Source: "shell setup"},
	{Name: "NO_COLOR", Group: "Editor And Terminal", Description: "Disable color where respected.", Source: "output"},
	{Name: "NOCOLOR", Group: "Editor And Terminal", Description: "Disable color; supported as an older spelling alongside `NO_COLOR`.", Source: "output"},
	{Name: "COLOR", Group: "Editor And Terminal", Description: "Force color where respected.", Source: "output"},
	{Name: "RSH_COMMAND_PLUGIN_DISCOVERY_TIMEOUT", Group: "Plugin Runtime", Description: "Override command-plugin startup discovery timeout.", Source: "plugin runtime"},
	{Name: "RSH_COMMAND_PLUGIN_SHUTDOWN_GRACE", Group: "Plugin Runtime", Description: "Override command-plugin shutdown grace period.", Source: "plugin runtime"},
	{Name: "GITHUB_TOKEN", Group: "Plugin Installation", Description: "Bearer token used for GitHub release API requests during `restish plugin install owner/repo plugin`.", Source: "plugin install"},
	{Name: "HTTPS_PROXY", Group: "Proxies", Description: "Standard Go HTTPS proxy setting used by Restish HTTP transports.", Source: "Go HTTP transport"},
	{Name: "HTTP_PROXY", Group: "Proxies", Description: "Standard Go HTTP proxy setting used by Restish HTTP transports.", Source: "Go HTTP transport"},
	{Name: "NO_PROXY", Group: "Proxies", Description: "Standard Go proxy bypass list used by Restish HTTP transports.", Source: "Go HTTP transport"},
}

func renderEnvironmentVariablesRegion(root string) (string, error) {
	if err := checkEnvDocCoverage(root); err != nil {
		return "", err
	}
	order := []string{
		"Config And Profiles",
		"Request Defaults",
		"Editor And Terminal",
		"Plugin Runtime",
		"Plugin Installation",
		"Proxies",
	}
	byGroup := map[string][]envDoc{}
	for _, doc := range envDocs {
		byGroup[doc.Group] = append(byGroup[doc.Group], doc)
	}
	var out strings.Builder
	out.WriteString("Generated from production source environment-variable usage plus Go's standard proxy environment contract.\n")
	for _, group := range order {
		docs := byGroup[group]
		if len(docs) == 0 {
			continue
		}
		out.WriteString("\n### ")
		out.WriteString(group)
		out.WriteString("\n\n")
		out.WriteString("| Variable | Purpose | Source |\n")
		out.WriteString("| --- | --- | --- |\n")
		for _, doc := range docs {
			out.WriteString("| `")
			out.WriteString(mdCode(doc.Name))
			out.WriteString("` | ")
			out.WriteString(md(doc.Description))
			out.WriteString(" | ")
			out.WriteString(md(doc.Source))
			out.WriteString(" |\n")
		}
	}
	return out.String(), nil
}

func checkEnvDocCoverage(root string) error {
	seen, err := sourceEnvNames(root)
	if err != nil {
		return err
	}
	documented := map[string]bool{}
	for _, doc := range envDocs {
		documented[doc.Name] = true
	}
	var missing []string
	for name := range seen {
		if !documented[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("environment reference missing docs for source variables: %s", strings.Join(missing, ", "))
	}
	return nil
}

func sourceEnvNames(root string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, dir := range []string{"internal", "cmd", "plugin"} {
		base := filepath.Join(root, dir)
		if err := filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				if dir == "cmd" && entry.Name() == "restish-docgen" {
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			return collectSourceEnvNames(path, out)
		}); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func collectSourceEnvNames(path string, out map[string]bool) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return err
	}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "os" {
			return true
		}
		switch sel.Sel.Name {
		case "Getenv", "LookupEnv":
			if name, ok := stringLiteral(call.Args[0]); ok {
				out[name] = true
			}
		}
		return true
	})
	return nil
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

type constDoc struct {
	Name  string
	Value string
}

func loadPluginDocs(root string) (map[string]structDoc, []constDoc, error) {
	structs, err := loadStructDocs(root, []string{"plugin/manifest.go", "plugin/messages.go"})
	if err != nil {
		return nil, nil, err
	}
	fset := token.NewFileSet()
	var constants []constDoc
	for _, rel := range []string{"plugin/manifest.go", "plugin/messages.go"} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, nil, err
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			switch gen.Tok {
			case token.CONST:
				for _, spec := range gen.Specs {
					valueSpec, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, name := range valueSpec.Names {
						if !strings.HasPrefix(name.Name, "MsgType") {
							continue
						}
						value := ""
						if i < len(valueSpec.Values) {
							if lit, ok := valueSpec.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
								value, _ = strconv.Unquote(lit.Value)
							}
						}
						constants = append(constants, constDoc{Name: name.Name, Value: value})
					}
				}
			}
		}
	}
	sort.Slice(constants, func(i, j int) bool {
		return constants[i].Name < constants[j].Name
	})
	return structs, constants, nil
}

func loadStructDocs(root string, rels []string) (map[string]structDoc, error) {
	fset := token.NewFileSet()
	structs := map[string]structDoc{}
	for _, rel := range rels {
		path := filepath.Join(root, filepath.FromSlash(rel))
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					continue
				}
				comment := commentText(typeSpec.Doc)
				if comment == "" {
					comment = commentText(gen.Doc)
				}
				structs[typeSpec.Name.Name] = structDoc{
					Name:        typeSpec.Name.Name,
					Description: comment,
					Fields:      parseFields(fset, st),
				}
			}
		}
	}
	return structs, nil
}

func parseFields(fset *token.FileSet, st *ast.StructType) []fieldDoc {
	var fields []fieldDoc
	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			continue
		}
		fieldType := exprString(fset, field.Type)
		tag := ""
		if field.Tag != nil {
			tag, _ = strconv.Unquote(field.Tag.Value)
		}
		cborName, cborRequired := tagName(tag, "cbor")
		jsonName, jsonRequired := tagName(tag, "json")
		desc := strings.TrimSpace(commentText(field.Doc) + " " + commentText(field.Comment))
		for _, name := range field.Names {
			if !name.IsExported() {
				continue
			}
			fields = append(fields, fieldDoc{
				Name:        name.Name,
				CBOR:        cborName,
				JSON:        jsonName,
				Type:        fieldType,
				Required:    cborRequired || (cborName == "" && jsonRequired),
				Description: desc,
			})
		}
	}
	return fields
}

func tagName(raw, key string) (string, bool) {
	tag := reflect.StructTag(raw)
	value := tag.Get(key)
	if value == "" || value == "-" {
		return "", false
	}
	parts := strings.Split(value, ",")
	required := true
	for _, part := range parts[1:] {
		if part == "omitempty" {
			required = false
			break
		}
	}
	return parts[0], required
}

func exprString(fset *token.FileSet, expr ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, expr); err != nil {
		return ""
	}
	formatted, err := format.Source([]byte("package p\nvar _ " + buf.String()))
	if err == nil {
		text := strings.TrimSpace(string(formatted))
		text = strings.TrimPrefix(text, "package p\n\nvar _ ")
		text = strings.TrimSpace(text)
		if text != "" {
			return text
		}
	}
	return buf.String()
}

func renderStructDoc(out *strings.Builder, doc structDoc) {
	out.WriteString("\n### `")
	out.WriteString(doc.Name)
	out.WriteString("`\n\n")
	if doc.Description != "" {
		out.WriteString(mdParagraph(doc.Description))
		out.WriteString("\n\n")
	}
	for _, field := range doc.Fields {
		out.WriteString("**`")
		out.WriteString(field.Name)
		out.WriteString("`**\n\n")
		var metadata []string
		if field.CBOR != "" {
			metadata = append(metadata, "CBOR: `"+mdCode(field.CBOR)+"`")
		}
		if field.JSON != "" {
			metadata = append(metadata, "JSON: `"+mdCode(field.JSON)+"`")
		}
		metadata = append(metadata, "type: `"+mdCode(field.Type)+"`")
		metadata = append(metadata, "required: "+boolWord(field.Required))
		out.WriteString(strings.Join(metadata, "; "))
		out.WriteString("\n\n")
		if field.Description != "" {
			out.WriteString(mdParagraph(field.Description))
			out.WriteString("\n\n")
		}
	}
}

func commentText(group *ast.CommentGroup) string {
	if group == nil {
		return ""
	}
	return strings.Join(strings.Fields(group.Text()), " ")
}

func boolWord(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func mdMaybeCode(value string) string {
	if value == "" {
		return "-"
	}
	return "`" + mdCode(value) + "`"
}

func mdParagraph(value string) string {
	return strings.TrimSpace(value)
}

func md(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	value = strings.ReplaceAll(value, "|", "\\|")
	if value == "" {
		return "-"
	}
	return value
}

func mdCode(value string) string {
	value = strings.ReplaceAll(value, "`", "\\`")
	value = strings.ReplaceAll(value, "|", "\\|")
	return value
}

func escapePipes(value string) string {
	return strings.ReplaceAll(value, "|", "\\|")
}
