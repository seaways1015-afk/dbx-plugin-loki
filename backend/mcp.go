package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Tools exposed to the DBX AI agent (and, when enabled by the user, the
// external dbx MCP server). All of them are read-only.

const mcpEntryLimit = 200
const mcpLineLimit = 2000

func mcpTools() map[string]any {
	readOnly := map[string]any{"readOnlyHint": true}
	lookback := map[string]any{"type": "string", "description": "How far back to search, e.g. 15m, 1h, 24h, 7d. Defaults to 1h."}
	return map[string]any{"tools": []map[string]any{
		{
			"name":        "loki_labels",
			"description": "List the label names present in Loki over a time window. Use it first to discover how log streams are labeled (app, namespace, pod, level, ...).",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"since": lookback}},
			"annotations": readOnly,
		},
		{
			"name":        "loki_label_values",
			"description": "List the values of one Loki label over a time window, e.g. every app or namespace that produced logs.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"label": map[string]any{"type": "string", "description": "Label name, e.g. app"}, "since": lookback},
				"required":   []string{"label"},
			},
			"annotations": readOnly,
		},
		{
			"name":        "loki_query_logs",
			"description": "Run a LogQL log query and return matching log lines, newest first. Example: {app=\"api\"} |= \"error\". Always include a stream selector in braces.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "LogQL log query"},
					"since": lookback,
					"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": mcpEntryLimit, "description": "Maximum lines to return (default 50)"},
				},
				"required": []string{"query"},
			},
			"annotations": readOnly,
		},
		{
			"name":        "loki_query_metrics",
			"description": "Run a LogQL metric query (rate, count_over_time, sum by, ...) evaluated at the current instant, e.g. sum by (level) (count_over_time({app=\"api\"}[5m])).",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"query": map[string]any{"type": "string", "description": "LogQL metric query"}},
				"required":   []string{"query"},
			},
			"annotations": readOnly,
		},
	}}
}

type mcpArguments struct {
	Query string `json:"query"`
	Label string `json:"label"`
	Since string `json:"since"`
	Limit int    `json:"limit"`
}

func (p *plugin) mcpCall(input request) map[string]any {
	text, err := p.runTool(input)
	if err != nil {
		return map[string]any{"content": []map[string]any{{"type": "text", "text": err.Error()}}, "isError": true}
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": false}
}

func (p *plugin) runTool(input request) (string, error) {
	var args mcpArguments
	if len(input.Arguments) > 0 {
		if err := json.Unmarshal(input.Arguments, &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	s, release, err := p.toolSession(input)
	if err != nil {
		return "", err
	}
	defer release()
	now := p.now()
	form := queryForm{Query: args.Query, Label: args.Label, Since: args.Since}

	switch input.Tool {
	case "loki_labels":
		names, err := s.labels(form, now)
		if err != nil {
			return "", err
		}
		return toJSON(map[string]any{"labels": names})
	case "loki_label_values":
		result, err := s.labelValues(form, now)
		if err != nil {
			return "", err
		}
		return toJSON(result)
	case "loki_query_logs":
		form.Limit = args.Limit
		if form.Limit <= 0 {
			form.Limit = 50
		}
		if form.Limit > mcpEntryLimit {
			form.Limit = mcpEntryLimit
		}
		result, err := s.queryRange(form, now)
		if err != nil {
			return "", err
		}
		return toJSON(summarizeForModel(result))
	case "loki_query_metrics":
		result, err := s.queryInstant(form, now)
		if err != nil {
			return "", err
		}
		return toJSON(summarizeForModel(result))
	default:
		return "", fmt.Errorf("unknown tool %q", input.Tool)
	}
}

// toolSession prefers the lifecycle payload the host attaches to each call
// and falls back to the session opened by the workbench.
func (p *plugin) toolSession(input request) (*session, func(), error) {
	if input.Lifecycle != nil && input.Lifecycle.Connection.Host != "" {
		s, err := newSession(input.Lifecycle.Connection, input.Lifecycle.Runtime)
		if err != nil {
			return nil, nil, err
		}
		return s, s.close, nil
	}
	id := input.ConnectionID
	if id == "" && input.Lifecycle != nil {
		id = input.Lifecycle.Connection.ID
	}
	p.mu.RLock()
	s := p.sessions[id]
	p.mu.RUnlock()
	if s == nil {
		return nil, nil, fmt.Errorf("the Loki connection is not open")
	}
	return s, func() {}, nil
}

// summarizeForModel trims results into a compact shape: models do not need
// stream ids or per-entry duplicated labels.
func summarizeForModel(result *logsResult) map[string]any {
	out := map[string]any{"resultType": result.ResultType, "truncated": result.Truncated}
	if result.ResultType == "streams" {
		lines := make([]map[string]any, 0, len(result.Entries))
		for _, entry := range result.Entries {
			line := entry.Line
			if len(line) > mcpLineLimit {
				line = truncateUTF8(line, mcpLineLimit) + "…"
			}
			lines = append(lines, map[string]any{"time": formatNanos(entry.Timestamp), "labels": entry.Labels, "line": line})
		}
		out["lines"] = lines
		out["count"] = len(lines)
		return out
	}
	if result.Scalar != nil {
		out["scalar"] = result.Scalar.Value
	}
	series := make([]map[string]any, 0, len(result.Series))
	for _, serie := range result.Series {
		item := map[string]any{"labels": serie.Labels}
		if n := len(serie.Points); n > 0 {
			item["value"] = serie.Points[n-1].Value
		}
		series = append(series, item)
	}
	out["series"] = series
	return out
}

func formatNanos(value string) string {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return value
	}
	return time.Unix(0, n).UTC().Format(time.RFC3339Nano)
}

func toJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(encoded)), nil
}
