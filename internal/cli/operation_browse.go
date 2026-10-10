package cli

import (
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/rest-sh/restish/v2/config"
	"github.com/rest-sh/restish/v2/internal/spec"
	"github.com/spf13/cobra"
)

type operationBrowseRow struct {
	Command     string `json:"command"`
	Summary     string `json:"summary"`
	OperationID string `json:"operation_id"`
}

func (c *CLI) browseGenericOperations(cmd *cobra.Command, method, rawURL string) (bool, error) {
	if c.cfg == nil {
		return false, nil
	}
	apiName, suffix := splitAPIShortNameSuffix(rawURL)
	apiCfg := c.cfg.APIs[apiName]
	if apiCfg == nil || strings.ContainsAny(suffix, "?#") {
		return false, nil
	}

	profileName := c.profileFromCmd(cmd)
	if profileName != "default" && profileForName(apiCfg, profileName) == nil {
		return false, nil
	}
	set, ok := c.completionOperationSet(cmd, apiName, apiCfg, profileName)
	if !ok {
		return false, nil
	}

	browsePath := apiName
	forceParentBrowse := false
	if suffix != "" {
		parsed, err := url.Parse(suffix)
		if err != nil {
			return false, nil
		}
		escapedPath := parsed.EscapedPath()
		forceParentBrowse = strings.HasSuffix(escapedPath, "/")
		browsePath += "/" + strings.TrimLeft(escapedPath, "/")
	}
	browsePath = strings.TrimRight(browsePath, "/")

	var rows []operationBrowseRow
	commandName := cmd.Root().Name()
	for _, op := range set.Operations {
		if !strings.EqualFold(op.Method, method) {
			continue
		}
		template, commandTarget := operationBrowsePaths(apiName, apiCfg, profileName, op)
		if template == "" {
			continue
		}
		resolved, relation := matchOperationBrowsePath(template, browsePath)
		if relation == operationBrowseExact {
			if !forceParentBrowse {
				return false, nil
			}
			continue
		}
		if relation != operationBrowseDescendant {
			continue
		}
		if op.OperationServer == "" {
			commandTarget = resolved
		} else {
			commandTarget = applyResolvedBrowseParameters(template, resolved, commandTarget)
		}
		rows = append(rows, operationBrowseRow{
			Command:     strings.Join([]string{commandName, strings.ToLower(method), commandTarget}, " "),
			Summary:     op.Summary,
			OperationID: op.ID,
		})
	}
	if len(rows) == 0 {
		return false, nil
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Command == rows[j].Command {
			return rows[i].OperationID < rows[j].OperationID
		}
		return rows[i].Command < rows[j].Command
	})
	outputFormat := globalFlagsFromContext(requestContext(cmd)).OutputFormat
	if c.stdoutIsTerminal() && (outputFormat == "" || outputFormat == "auto") {
		return true, writeOperationBrowseTable(c.Stdout, rows)
	}
	return true, c.renderValue(cmd, operationBrowseValues(rows), false)
}

func operationBrowsePaths(apiName string, apiCfg *config.APIConfig, profileName string, op spec.Operation) (string, string) {
	baseURL, operationBase := completionOperationBase(apiCfg, profileName)
	shortPath := shortOperationCompletionPath(baseURL, operationBase, op.Path)
	if shortPath == "" {
		return "", ""
	}
	shortPath = strings.ReplaceAll(shortPath, "%7B", "{")
	shortPath = strings.ReplaceAll(shortPath, "%7D", "}")
	template := apiName + "/" + strings.TrimLeft(shortPath, "/")
	if op.OperationServer == "" {
		return template, template
	}
	return template, cleanCompletionURL(strings.TrimRight(op.OperationServer, "/") + op.Path)
}

type operationBrowseRelation uint8

const (
	operationBrowseUnrelated operationBrowseRelation = iota
	operationBrowseExact
	operationBrowseDescendant
)

func matchOperationBrowsePath(template, browsePath string) (string, operationBrowseRelation) {
	templateParts := splitBrowsePath(template)
	browseParts := splitBrowsePath(browsePath)
	if len(browseParts) > len(templateParts) {
		return "", operationBrowseUnrelated
	}

	resolved := append([]string(nil), templateParts...)
	for i, part := range browseParts {
		if isPathTemplateSegment(templateParts[i]) {
			resolved[i] = part
			continue
		}
		if templateParts[i] != part {
			return "", operationBrowseUnrelated
		}
	}
	if len(browseParts) == len(templateParts) {
		return strings.Join(resolved, "/"), operationBrowseExact
	}
	return strings.Join(resolved, "/"), operationBrowseDescendant
}

func splitBrowsePath(value string) []string {
	value = strings.Trim(value, "/")
	if value == "" {
		return nil
	}
	return strings.Split(value, "/")
}

func isPathTemplateSegment(value string) bool {
	return strings.Contains(value, "{") && strings.Contains(value, "}")
}

func applyResolvedBrowseParameters(template, resolved, target string) string {
	templateParts := splitBrowsePath(template)
	resolvedParts := splitBrowsePath(resolved)
	for i, part := range templateParts {
		if i >= len(resolvedParts) || !isPathTemplateSegment(part) {
			continue
		}
		target = strings.ReplaceAll(target, part, resolvedParts[i])
	}
	return target
}

func operationBrowseValues(rows []operationBrowseRow) []any {
	values := make([]any, 0, len(rows))
	for _, row := range rows {
		values = append(values, map[string]any{
			"command":      row.Command,
			"summary":      row.Summary,
			"operation_id": row.OperationID,
		})
	}
	return values
}

func writeOperationBrowseTable(w io.Writer, rows []operationBrowseRow) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "COMMAND\tSUMMARY\tOPERATION ID"); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\n", row.Command, browseTableCell(row.Summary), browseTableCell(row.OperationID)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func browseTableCell(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
