package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxQueryLength   = 8192
	defaultLogLimit  = 500
	maxLogLimit      = 5000
	maxLineBytes     = 16 << 10
	maxResultBytes   = 4 << 20
	maxSeries        = 200
	maxPoints        = 11000
	defaultLookback  = time.Hour
	maxLabelValues   = 5000
	histogramBuckets = 60
)

var labelNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// queryForm is the parameter object sent by the workbench UI and translated
// from MCP tool arguments. Times are Unix nanoseconds encoded as strings so
// that JavaScript can carry them without precision loss.
type queryForm struct {
	Query     string `json:"query"`
	Label     string `json:"label"`
	Start     string `json:"start"`
	End       string `json:"end"`
	Time      string `json:"time"`
	Since     string `json:"since"`
	Limit     int    `json:"limit"`
	Direction string `json:"direction"`
	Step      int    `json:"step"`
}

type timeRange struct {
	start int64
	end   int64
}

type logEntry struct {
	Timestamp string            `json:"ts"`
	Line      string            `json:"line"`
	Labels    map[string]string `json:"labels"`
	StreamID  int               `json:"stream"`
	Truncated bool              `json:"truncated,omitempty"`
}

type logsResult struct {
	ResultType string        `json:"resultType"`
	Entries    []logEntry    `json:"entries,omitempty"`
	Series     []metricSerie `json:"series,omitempty"`
	Scalar     *samplePoint  `json:"scalar,omitempty"`
	Truncated  bool          `json:"truncated"`
	Stats      queryStats    `json:"stats"`
}

type metricSerie struct {
	Labels map[string]string `json:"labels"`
	Points []samplePoint     `json:"points"`
}

type samplePoint struct {
	Time  float64 `json:"t"`
	Value string  `json:"v"`
}

type queryStats struct {
	Streams        int     `json:"streams"`
	Entries        int     `json:"entries"`
	BytesProcessed int64   `json:"bytesProcessed"`
	ExecTime       float64 `json:"execTimeSeconds"`
}

type histogramResult struct {
	Step    int           `json:"step"`
	Buckets []samplePoint `json:"buckets"`
	Total   float64       `json:"total"`
}

func validateQuery(query string) (string, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return "", errors.New("LogQL query is empty")
	}
	if len(q) > maxQueryLength {
		return "", fmt.Errorf("LogQL query exceeds %d characters", maxQueryLength)
	}
	return q, nil
}

func parseNanos(value string) (int64, bool, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return 0, false, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 0, false, errors.New("invalid timestamp")
	}
	return n, true, nil
}

// resolveRange turns start/end/since into a validated nanosecond window. When
// neither start nor since is given it defaults to the last hour.
func resolveRange(form queryForm, now time.Time) (timeRange, error) {
	end, hasEnd, err := parseNanos(form.End)
	if err != nil {
		return timeRange{}, err
	}
	if !hasEnd {
		end = now.UnixNano()
	}
	start, hasStart, err := parseNanos(form.Start)
	if err != nil {
		return timeRange{}, err
	}
	if !hasStart {
		lookback := defaultLookback
		if strings.TrimSpace(form.Since) != "" {
			parsed, err := parseLookback(form.Since)
			if err != nil {
				return timeRange{}, err
			}
			lookback = parsed
		}
		start = end - lookback.Nanoseconds()
	}
	if start >= end {
		return timeRange{}, errors.New("start must be before end")
	}
	if end > now.Add(time.Hour).UnixNano() {
		return timeRange{}, errors.New("end is too far in the future")
	}
	return timeRange{start: start, end: end}, nil
}

// parseLookback accepts Go durations plus Loki-style day/week units (1d, 2w).
func parseLookback(value string) (time.Duration, error) {
	v := strings.TrimSpace(value)
	multiplier := time.Duration(0)
	switch {
	case strings.HasSuffix(v, "d"):
		multiplier = 24 * time.Hour
	case strings.HasSuffix(v, "w"):
		multiplier = 7 * 24 * time.Hour
	}
	if multiplier != 0 {
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSuffix(v, "d"), "w"))
		if err != nil || n <= 0 || n > 400 {
			return 0, errors.New("invalid lookback duration")
		}
		return time.Duration(n) * multiplier, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, errors.New("invalid lookback duration; use values such as 15m, 6h or 7d")
	}
	return d, nil
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return defaultLogLimit
	}
	if limit > maxLogLimit {
		return maxLogLimit
	}
	return limit
}

func nanos(value int64) string { return strconv.FormatInt(value, 10) }

func (s *session) buildInfo() (map[string]any, error) {
	body, err := s.get("/loki/api/v1/status/buildinfo", nil)
	if err != nil {
		return nil, err
	}
	var info map[string]any
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, errors.New("Loki build info is not valid JSON; check the path prefix")
	}
	return info, nil
}

// ping checks that the API answers and that the credentials and tenant are
// accepted. Build info is optional because gateways (e.g. Grafana Cloud)
// often do not expose it, so a label request is the authoritative check.
func (s *session) ping(now time.Time) (string, error) {
	values := url.Values{"start": {nanos(now.Add(-5 * time.Minute).UnixNano())}, "end": {nanos(now.UnixNano())}}
	if err := s.getData("/loki/api/v1/labels", values, nil); err != nil {
		return "", err
	}
	if info, err := s.buildInfo(); err == nil {
		if version, ok := info["version"].(string); ok && version != "" {
			return version, nil
		}
	}
	return "", nil
}

func (s *session) labels(form queryForm, now time.Time) ([]string, error) {
	window, err := resolveRange(form, now)
	if err != nil {
		return nil, err
	}
	values := url.Values{"start": {nanos(window.start)}, "end": {nanos(window.end)}}
	if q := strings.TrimSpace(form.Query); q != "" {
		if _, err := validateQuery(q); err != nil {
			return nil, err
		}
		values.Set("query", q)
	}
	var names []string
	if err := s.getData("/loki/api/v1/labels", values, &names); err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

func (s *session) labelValues(form queryForm, now time.Time) (map[string]any, error) {
	if !labelNamePattern.MatchString(form.Label) {
		return nil, errors.New("invalid label name")
	}
	window, err := resolveRange(form, now)
	if err != nil {
		return nil, err
	}
	values := url.Values{"start": {nanos(window.start)}, "end": {nanos(window.end)}}
	if q := strings.TrimSpace(form.Query); q != "" {
		if _, err := validateQuery(q); err != nil {
			return nil, err
		}
		values.Set("query", q)
	}
	var items []string
	if err := s.getData("/loki/api/v1/label/"+url.PathEscape(form.Label)+"/values", values, &items); err != nil {
		return nil, err
	}
	sort.Strings(items)
	truncated := false
	if len(items) > maxLabelValues {
		items, truncated = items[:maxLabelValues], true
	}
	return map[string]any{"label": form.Label, "values": items, "truncated": truncated}, nil
}

type rawQueryData struct {
	ResultType string          `json:"resultType"`
	Result     json.RawMessage `json:"result"`
	Stats      struct {
		Summary struct {
			TotalBytesProcessed int64   `json:"totalBytesProcessed"`
			ExecTime            float64 `json:"execTime"`
		} `json:"summary"`
	} `json:"stats"`
}

// queryRange runs /query_range. Log queries are flattened into a single
// timestamp-ordered entry list; metric queries are returned as series.
func (s *session) queryRange(form queryForm, now time.Time) (*logsResult, error) {
	q, err := validateQuery(form.Query)
	if err != nil {
		return nil, err
	}
	window, err := resolveRange(form, now)
	if err != nil {
		return nil, err
	}
	direction := form.Direction
	if direction == "" {
		direction = "backward"
	}
	if direction != "backward" && direction != "forward" {
		return nil, errors.New("direction must be backward or forward")
	}
	limit := clampLimit(form.Limit)
	values := url.Values{
		"query":     {q},
		"start":     {nanos(window.start)},
		"end":       {nanos(window.end)},
		"limit":     {strconv.Itoa(limit)},
		"direction": {direction},
	}
	if form.Step > 0 {
		if (window.end-window.start)/(int64(form.Step)*int64(time.Second)) > maxPoints {
			return nil, fmt.Errorf("step is too small for the selected range (max %d points)", maxPoints)
		}
		values.Set("step", strconv.Itoa(form.Step)+"s")
	}
	var data rawQueryData
	if err := s.getData("/loki/api/v1/query_range", values, &data); err != nil {
		return nil, err
	}
	return normalizeQueryData(data, direction)
}

// queryInstant runs /query for metric queries evaluated at a single instant.
func (s *session) queryInstant(form queryForm, now time.Time) (*logsResult, error) {
	q, err := validateQuery(form.Query)
	if err != nil {
		return nil, err
	}
	at, hasTime, err := parseNanos(form.Time)
	if err != nil {
		return nil, err
	}
	if !hasTime {
		at = now.UnixNano()
	}
	values := url.Values{"query": {q}, "time": {nanos(at)}, "limit": {strconv.Itoa(clampLimit(form.Limit))}}
	var data rawQueryData
	if err := s.getData("/loki/api/v1/query", values, &data); err != nil {
		return nil, err
	}
	return normalizeQueryData(data, "backward")
}

func normalizeQueryData(data rawQueryData, direction string) (*logsResult, error) {
	result := &logsResult{ResultType: data.ResultType}
	result.Stats.BytesProcessed = data.Stats.Summary.TotalBytesProcessed
	result.Stats.ExecTime = data.Stats.Summary.ExecTime
	switch data.ResultType {
	case "streams":
		var streams []struct {
			Stream map[string]string   `json:"stream"`
			Values [][]json.RawMessage `json:"values"`
		}
		if err := json.Unmarshal(data.Result, &streams); err != nil {
			return nil, errors.New("Loki returned malformed streams")
		}
		result.Stats.Streams = len(streams)
		for index, stream := range streams {
			for _, value := range stream.Values {
				if len(value) < 2 {
					continue
				}
				var ts, line string
				if json.Unmarshal(value[0], &ts) != nil || json.Unmarshal(value[1], &line) != nil {
					continue
				}
				entry := logEntry{Timestamp: ts, Line: line, Labels: stream.Stream, StreamID: index}
				if len(entry.Line) > maxLineBytes {
					entry.Line, entry.Truncated = truncateUTF8(entry.Line, maxLineBytes), true
				}
				result.Entries = append(result.Entries, entry)
			}
		}
		sortEntries(result.Entries, direction)
		result.Stats.Entries = len(result.Entries)
		result.Entries, result.Truncated = capEntries(result.Entries)
	case "matrix", "vector":
		var series []struct {
			Metric map[string]string   `json:"metric"`
			Values [][]json.RawMessage `json:"values"`
			Value  []json.RawMessage   `json:"value"`
		}
		if err := json.Unmarshal(data.Result, &series); err != nil {
			return nil, errors.New("Loki returned malformed series")
		}
		for _, item := range series {
			if len(result.Series) >= maxSeries {
				result.Truncated = true
				break
			}
			serie := metricSerie{Labels: item.Metric}
			if item.Metric == nil {
				serie.Labels = map[string]string{}
			}
			raw := item.Values
			if data.ResultType == "vector" && len(item.Value) == 2 {
				raw = [][]json.RawMessage{item.Value}
			}
			for _, pair := range raw {
				if point, ok := parsePoint(pair); ok {
					serie.Points = append(serie.Points, point)
				}
			}
			result.Series = append(result.Series, serie)
		}
		result.Stats.Streams = len(series)
	case "scalar":
		var pair []json.RawMessage
		if err := json.Unmarshal(data.Result, &pair); err != nil {
			return nil, errors.New("Loki returned a malformed scalar")
		}
		if point, ok := parsePoint(pair); ok {
			result.Scalar = &point
		}
	default:
		return nil, fmt.Errorf("unsupported Loki result type %q", data.ResultType)
	}
	return result, nil
}

func parsePoint(pair []json.RawMessage) (samplePoint, bool) {
	if len(pair) != 2 {
		return samplePoint{}, false
	}
	var t float64
	var v string
	if json.Unmarshal(pair[0], &t) != nil || json.Unmarshal(pair[1], &v) != nil {
		return samplePoint{}, false
	}
	return samplePoint{Time: t, Value: v}, true
}

// sortEntries orders entries across streams. Timestamps are nanosecond
// strings, so compare by length first to avoid parsing.
func sortEntries(entries []logEntry, direction string) {
	less := func(a, b string) bool {
		if len(a) != len(b) {
			return len(a) < len(b)
		}
		return a < b
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if direction == "forward" {
			return less(entries[i].Timestamp, entries[j].Timestamp)
		}
		return less(entries[j].Timestamp, entries[i].Timestamp)
	})
}

// capEntries keeps the encoded result below maxResultBytes so that it fits
// into a single sidecar response message.
func capEntries(entries []logEntry) ([]logEntry, bool) {
	total := 0
	for index, entry := range entries {
		total += len(entry.Line) + len(entry.Timestamp) + 64
		for key, value := range entry.Labels {
			total += len(key) + len(value) + 6
		}
		if total > maxResultBytes {
			return entries[:index], true
		}
	}
	return entries, false
}

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

// histogram counts matching log lines per bucket by wrapping the log query in
// count_over_time. Metric queries cannot be wrapped and return an error the
// UI treats as "no histogram".
//
// Loki snaps query_range evaluation times to multiples of the step, so a range
// query over an unaligned window would count up to one step of lines before
// the window and after it. The full buckets between the first and last step
// boundaries inside the window are therefore read with query_range, and the
// partial buckets at either edge with exact instant queries. Each bucket is
// stamped at its end; the UI draws it from the previous step boundary.
func (s *session) histogram(form queryForm, now time.Time) (*histogramResult, error) {
	q, err := validateQuery(form.Query)
	if err != nil {
		return nil, err
	}
	window, err := resolveRange(form, now)
	if err != nil {
		return nil, err
	}
	step := histogramStep(window)
	stepNs := int64(step) * int64(time.Second)
	first := (window.start + stepNs - 1) / stepNs * stepNs // first boundary at or after start
	last := window.end / stepNs * stepNs                   // last boundary at or before end

	result := &histogramResult{Step: step, Buckets: []samplePoint{}}
	add := func(points []samplePoint) {
		for _, point := range points {
			result.Buckets = append(result.Buckets, point)
			if v, err := strconv.ParseFloat(point.Value, 64); err == nil {
				result.Total += v
			}
		}
	}
	// (start, min(first, end)]: the head, or the whole window when no boundary falls inside it.
	headEnd := min(first, window.end)
	points, err := s.countInstant(q, window.start, headEnd)
	if err != nil {
		return nil, err
	}
	add(points)
	if last-first >= stepNs {
		values := url.Values{
			"query": {fmt.Sprintf("sum(count_over_time(%s [%ds]))", q, step)},
			"start": {nanos(first + stepNs)},
			"end":   {nanos(last)},
			"step":  {strconv.Itoa(step) + "s"},
		}
		var data rawQueryData
		if err := s.getData("/loki/api/v1/query_range", values, &data); err != nil {
			return nil, err
		}
		normalized, err := normalizeQueryData(data, "forward")
		if err != nil {
			return nil, err
		}
		for _, serie := range normalized.Series {
			add(serie.Points)
		}
	}
	if first <= last && window.end > last {
		points, err := s.countInstant(q, last, window.end)
		if err != nil {
			return nil, err
		}
		add(points)
	}
	return result, nil
}

// countInstant counts the lines in (from, to] with one instant query stamped
// at `to`. Spans shorter than a millisecond are skipped.
func (s *session) countInstant(q string, from, to int64) ([]samplePoint, error) {
	spanMs := (to - from) / int64(time.Millisecond)
	if spanMs <= 0 {
		return nil, nil
	}
	values := url.Values{"query": {fmt.Sprintf("sum(count_over_time(%s [%dms]))", q, spanMs)}, "time": {nanos(to)}}
	var data rawQueryData
	if err := s.getData("/loki/api/v1/query", values, &data); err != nil {
		return nil, err
	}
	normalized, err := normalizeQueryData(data, "forward")
	if err != nil {
		return nil, err
	}
	var points []samplePoint
	for _, serie := range normalized.Series {
		points = append(points, serie.Points...)
	}
	return points, nil
}

// histogramStep picks a "nice" bucket width yielding about histogramBuckets bars.
func histogramStep(window timeRange) int {
	seconds := float64(window.end-window.start) / float64(time.Second)
	raw := seconds / histogramBuckets
	for _, candidate := range []int{1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400} {
		if float64(candidate) >= raw {
			return candidate
		}
	}
	return int(math.Ceil(raw/86400)) * 86400
}

// tail returns entries newer than `start` in ascending order. The UI polls it
// to implement live tailing without holding a WebSocket inside the sidecar.
// The UI re-reads an overlap window to catch late lines, so the newest lines
// are fetched (backward): fetching forward could return only already-shown
// lines once the overlap holds more than the limit, and the tail would stall.
func (s *session) tail(form queryForm, now time.Time) (*logsResult, error) {
	form.Direction = "backward"
	if strings.TrimSpace(form.Start) == "" {
		form.Start = nanos(now.Add(-time.Minute).UnixNano())
	}
	form.End = nanos(now.UnixNano())
	if form.Limit <= 0 || form.Limit > 1000 {
		form.Limit = 1000
	}
	result, err := s.queryRange(form, now)
	if err != nil {
		return nil, err
	}
	sortEntries(result.Entries, "forward")
	return result, nil
}
