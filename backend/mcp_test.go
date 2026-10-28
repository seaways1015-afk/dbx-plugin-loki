package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMCPToolsAreReadOnly(t *testing.T) {
	tools := mcpTools()["tools"].([]map[string]any)
	if len(tools) != 4 {
		t.Fatalf("tools = %d", len(tools))
	}
	for _, tool := range tools {
		annotations := tool["annotations"].(map[string]any)
		if annotations["readOnlyHint"] != true {
			t.Errorf("%s is not read-only", tool["name"])
		}
	}
}

func TestMCPCallUsesLifecycleConnection(t *testing.T) {
	f := newFakeLoki(t, defaultHandler)
	p := newPlugin()
	p.now = func() time.Time { return fixedNow }
	args, _ := json.Marshal(map[string]any{"query": `{app="api"}`, "since": "30m", "limit": 999})
	result := p.mcpCall(request{Tool: "loki_query_logs", Arguments: args, Lifecycle: &lifecycle{Connection: f.connection()}})
	if result["isError"] != false {
		t.Fatalf("result = %v", result)
	}
	if got := f.last().URL.Query().Get("limit"); got != "200" {
		t.Fatalf("limit not clamped: %s", got)
	}
	text := result["content"].([]map[string]any)[0]["text"].(string)
	var payload struct {
		Count int `json:"count"`
		Lines []struct {
			Time string `json:"time"`
			Line string `json:"line"`
		} `json:"lines"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Count != 3 || payload.Lines[0].Line != "api new" || !strings.HasPrefix(payload.Lines[0].Time, "2027-01-15T") {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestMCPMetricsAndErrors(t *testing.T) {
	f := newFakeLoki(t, defaultHandler)
	p := newPlugin()
	p.now = func() time.Time { return fixedNow }
	args, _ := json.Marshal(map[string]any{"query": `sum by (level) (count_over_time({app="api"}[5m]))`})
	result := p.mcpCall(request{Tool: "loki_query_metrics", Arguments: args, Lifecycle: &lifecycle{Connection: f.connection()}})
	text := result["content"].([]map[string]any)[0]["text"].(string)
	if result["isError"] != false || !strings.Contains(text, `"value":"42"`) {
		t.Fatalf("result = %v", result)
	}

	result = p.mcpCall(request{Tool: "loki_nope", Lifecycle: &lifecycle{Connection: f.connection()}})
	if result["isError"] != true {
		t.Fatal("unknown tool must be an error result")
	}
	result = p.mcpCall(request{Tool: "loki_labels"})
	if result["isError"] != true {
		t.Fatal("missing connection must be an error result")
	}
}
