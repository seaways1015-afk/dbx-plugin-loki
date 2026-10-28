package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

var fixedNow = time.Unix(1_800_000_000, 0)

type fakeLoki struct {
	t        *testing.T
	server   *httptest.Server
	requests []*http.Request
}

func newFakeLoki(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *fakeLoki {
	f := &fakeLoki{t: t}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.Clone(r.Context()))
		handler(w, r)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeLoki) connection() connection {
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(f.server.URL, "http://"))
	p, _ := strconv.Atoi(port)
	return connection{ID: "c1", Name: "test", Host: host, Port: p}
}

func (f *fakeLoki) last() *http.Request { return f.requests[len(f.requests)-1] }

func success(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": data})
}

func openPlugin(t *testing.T, f *fakeLoki, c connection) *plugin {
	p := newPlugin()
	p.now = func() time.Time { return fixedNow }
	if _, err := p.handle("connection/connect", request{Connection: c}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return p
}

func defaultHandler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/loki/api/v1/labels":
		success(w, []string{"namespace", "app"})
	case "/loki/api/v1/status/buildinfo":
		_ = json.NewEncoder(w).Encode(map[string]any{"version": "3.5.0"})
	case "/loki/api/v1/label/app/values":
		success(w, []string{"web", "api"})
	case "/loki/api/v1/query_range":
		if strings.HasPrefix(r.URL.Query().Get("query"), "sum(count_over_time(") {
			success(w, map[string]any{"resultType": "matrix", "result": []any{
				map[string]any{"metric": map[string]string{}, "values": [][]any{{1799996400, "3"}, {1799996460, "4"}}},
			}})
			return
		}
		success(w, map[string]any{
			"resultType": "streams",
			"result": []any{
				map[string]any{"stream": map[string]string{"app": "api"}, "values": [][]string{{"1799999990000000000", "api old"}, {"1799999999000000000", "api new"}}},
				map[string]any{"stream": map[string]string{"app": "web"}, "values": [][]string{{"1799999995000000000", "web mid"}}},
			},
			"stats": map[string]any{"summary": map[string]any{"totalBytesProcessed": 1234, "execTime": 0.01}},
		})
	case "/loki/api/v1/query":
		success(w, map[string]any{"resultType": "vector", "result": []any{
			map[string]any{"metric": map[string]string{"level": "error"}, "value": []any{1800000000, "42"}},
		}})
	default:
		http.NotFound(w, r)
	}
}

func TestConnectionTestReportsVersion(t *testing.T) {
	f := newFakeLoki(t, defaultHandler)
	p := newPlugin()
	p.now = func() time.Time { return fixedNow }
	result, err := p.handle("connection/test", request{Connection: f.connection()})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.(map[string]any)["message"]; got != "Connected to Loki 3.5.0" {
		t.Fatalf("message = %v", got)
	}
}

func TestConnectionTestSurfacesAuthErrors(t *testing.T) {
	f := newFakeLoki(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no org id", http.StatusUnauthorized)
	})
	p := newPlugin()
	_, err := p.handle("connection/test", request{Connection: f.connection()})
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") || !strings.Contains(err.Error(), "no org id") {
		t.Fatalf("err = %v", err)
	}
}

func TestAuthAndTenantHeaders(t *testing.T) {
	f := newFakeLoki(t, defaultHandler)
	c := f.connection()
	c.Config = config{AuthMode: "bearer", Tenant: "team-a"}
	c.Secrets = map[string]string{"token": "s3cret"}
	openPlugin(t, f, c)
	r := f.last()
	if r.Header.Get("Authorization") != "Bearer s3cret" || r.Header.Get("X-Scope-OrgID") != "team-a" {
		t.Fatalf("headers = %v", r.Header)
	}

	c.Config = config{AuthMode: "basic"}
	c.Username, c.Password = "u", "p"
	openPlugin(t, f, c)
	if user, pass, ok := f.last().BasicAuth(); !ok || user != "u" || pass != "p" {
		t.Fatalf("basic auth missing")
	}
}

func TestSessionValidation(t *testing.T) {
	cases := []connection{
		{Host: "http://x", Port: 3100},
		{Host: "loki", Port: 0},
		{Host: "loki", Port: 3100, Config: config{Scheme: "ftp"}},
		{Host: "loki", Port: 3100, Config: config{ContextPath: "/../x"}},
		{Host: "loki", Port: 3100, Config: config{AuthMode: "basic"}},
		{Host: "loki", Port: 3100, Config: config{AuthMode: "bearer"}},
		{Host: "loki", Port: 3100, Config: config{Tenant: "a\nb"}},
	}
	for i, c := range cases {
		if _, err := newSession(c, runtimeEndpoint{}); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
	enabled := true
	tunnel := connection{Host: "loki", Port: 3100, TransportLayers: []transportLayer{{Type: "ssh", Enabled: &enabled}}}
	if _, err := newSession(tunnel, runtimeEndpoint{}); err == nil {
		t.Error("tunnel without runtime endpoint must fail closed")
	}
	if _, err := newSession(tunnel, runtimeEndpoint{Host: "127.0.0.1", Port: 4000}); err != nil {
		t.Errorf("tunnel with endpoint: %v", err)
	}
}

func TestContextPathPrefix(t *testing.T) {
	f := newFakeLoki(t, func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/gateway")
		defaultHandler(w, r)
	})
	c := f.connection()
	c.Config.ContextPath = "/gateway/"
	openPlugin(t, f, c)
	if !strings.HasPrefix(f.last().URL.Path, "/gateway/loki/api/v1/") {
		t.Fatalf("path = %s", f.last().URL.Path)
	}
}

func TestQueryRangeMergesStreamsNewestFirst(t *testing.T) {
	f := newFakeLoki(t, defaultHandler)
	p := openPlugin(t, f, f.connection())
	raw, err := p.handle("loki/query_range", request{ConnectionID: "c1", Form: queryForm{Query: `{app=~".+"}`, Since: "15m", Limit: 100}})
	if err != nil {
		t.Fatal(err)
	}
	result := raw.(*logsResult)
	var lines []string
	for _, entry := range result.Entries {
		lines = append(lines, entry.Line)
	}
	if strings.Join(lines, ",") != "api new,web mid,api old" {
		t.Fatalf("order = %v", lines)
	}
	if result.Stats.Streams != 2 || result.Stats.BytesProcessed != 1234 {
		t.Fatalf("stats = %+v", result.Stats)
	}
	q := f.last().URL.Query()
	if q.Get("limit") != "100" || q.Get("direction") != "backward" {
		t.Fatalf("query params = %v", q)
	}
	start, _ := strconv.ParseInt(q.Get("start"), 10, 64)
	end, _ := strconv.ParseInt(q.Get("end"), 10, 64)
	if time.Duration(end-start) != 15*time.Minute || end != fixedNow.UnixNano() {
		t.Fatalf("window = %d..%d", start, end)
	}
}

func TestQueryRangeValidation(t *testing.T) {
	f := newFakeLoki(t, defaultHandler)
	p := openPlugin(t, f, f.connection())
	bad := []queryForm{
		{Query: "  "},
		{Query: strings.Repeat("a", maxQueryLength+1)},
		{Query: "{a=\"b\"}", Since: "yesterday"},
		{Query: "{a=\"b\"}", Start: "200", End: "100"},
		{Query: "{a=\"b\"}", Direction: "sideways"},
	}
	for i, form := range bad {
		if _, err := p.handle("loki/query_range", request{ConnectionID: "c1", Form: form}); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}

func TestLokiErrorBodyIsShown(t *testing.T) {
	f := newFakeLoki(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/loki/api/v1/query_range" {
			http.Error(w, "parse error at line 1, col 2: syntax error: unexpected IDENTIFIER", http.StatusBadRequest)
			return
		}
		defaultHandler(w, r)
	})
	p := openPlugin(t, f, f.connection())
	_, err := p.handle("loki/query_range", request{ConnectionID: "c1", Form: queryForm{Query: "{x"}})
	if err == nil || !strings.Contains(err.Error(), "parse error at line 1") {
		t.Fatalf("err = %v", err)
	}
}

func TestLongLinesAreTruncated(t *testing.T) {
	long := strings.Repeat("é", maxLineBytes)
	f := newFakeLoki(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/loki/api/v1/query_range" {
			success(w, map[string]any{"resultType": "streams", "result": []any{
				map[string]any{"stream": map[string]string{"a": "b"}, "values": [][]string{{"1799999999000000000", long}}},
			}})
			return
		}
		defaultHandler(w, r)
	})
	p := openPlugin(t, f, f.connection())
	raw, err := p.handle("loki/query_range", request{ConnectionID: "c1", Form: queryForm{Query: `{a="b"}`}})
	if err != nil {
		t.Fatal(err)
	}
	entry := raw.(*logsResult).Entries[0]
	if !entry.Truncated || len(entry.Line) > maxLineBytes || !strings.HasPrefix(long, entry.Line) {
		t.Fatalf("truncated=%v len=%d", entry.Truncated, len(entry.Line))
	}
}

func TestLabelsAndValues(t *testing.T) {
	f := newFakeLoki(t, defaultHandler)
	p := openPlugin(t, f, f.connection())
	raw, err := p.handle("loki/labels", request{ConnectionID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := raw.(map[string]any)["labels"].([]string); strings.Join(got, ",") != "app,namespace" {
		t.Fatalf("labels = %v", got)
	}
	raw, err = p.handle("loki/label_values", request{ConnectionID: "c1", Form: queryForm{Label: "app"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := raw.(map[string]any)["values"].([]string); strings.Join(got, ",") != "api,web" {
		t.Fatalf("values = %v", got)
	}
	if _, err := p.handle("loki/label_values", request{ConnectionID: "c1", Form: queryForm{Label: "bad/label"}}); err == nil {
		t.Fatal("expected invalid label error")
	}
}

func TestHistogramWrapsQuery(t *testing.T) {
	f := newFakeLoki(t, defaultHandler)
	p := openPlugin(t, f, f.connection())
	raw, err := p.handle("loki/histogram", request{ConnectionID: "c1", Form: queryForm{Query: `{app="api"} |= "err"`, Since: "1h"}})
	if err != nil {
		t.Fatal(err)
	}
	result := raw.(*histogramResult)
	if result.Step != 60 || result.Total != 7 || len(result.Buckets) != 2 {
		t.Fatalf("histogram = %+v", result)
	}
	if got := f.last().URL.Query().Get("query"); got != `sum(count_over_time({app="api"} |= "err" [60s]))` {
		t.Fatalf("query = %s", got)
	}
}

func TestTailUsesForwardDirection(t *testing.T) {
	f := newFakeLoki(t, defaultHandler)
	p := openPlugin(t, f, f.connection())
	raw, err := p.handle("loki/tail", request{ConnectionID: "c1", Form: queryForm{Query: `{app=~".+"}`, Start: "1799999980000000000"}})
	if err != nil {
		t.Fatal(err)
	}
	if f.last().URL.Query().Get("direction") != "forward" {
		t.Fatal("tail must query forward")
	}
	entries := raw.(*logsResult).Entries
	if entries[0].Line != "api old" || entries[len(entries)-1].Line != "api new" {
		t.Fatalf("tail order = %+v", entries)
	}
}

func TestOperationsRequireOpenConnection(t *testing.T) {
	p := newPlugin()
	if _, err := p.handle("loki/labels", request{ConnectionID: "missing"}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := p.handle("loki/unknown", request{}); err == nil {
		t.Fatal("expected unknown method error")
	}
}

func TestDisconnectClosesSession(t *testing.T) {
	f := newFakeLoki(t, defaultHandler)
	p := openPlugin(t, f, f.connection())
	if _, err := p.handle("connection/disconnect", request{Connection: connection{ID: "c1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.handle("loki/labels", request{ConnectionID: "c1"}); err == nil {
		t.Fatal("expected closed connection")
	}
}

func TestHistogramStep(t *testing.T) {
	hour := timeRange{start: 0, end: int64(time.Hour)}
	if got := histogramStep(hour); got != 60 {
		t.Fatalf("1h step = %d", got)
	}
	week := timeRange{start: 0, end: int64(7 * 24 * time.Hour)}
	if got := histogramStep(week); got != 10800 {
		t.Fatalf("7d step = %d", got)
	}
}

func TestParseLookback(t *testing.T) {
	for input, want := range map[string]time.Duration{"15m": 15 * time.Minute, "6h": 6 * time.Hour, "7d": 7 * 24 * time.Hour, "2w": 14 * 24 * time.Hour} {
		got, err := parseLookback(input)
		if err != nil || got != want {
			t.Errorf("%s: got %v %v", input, got, err)
		}
	}
	for _, input := range []string{"", "-1h", "0d", "abc"} {
		if _, err := parseLookback(input); err == nil {
			t.Errorf("%q: expected error", input)
		}
	}
}
