package main

import (
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	sdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

const pluginID = "io.github.dbx-loki.loki"
const version = "0.1.0"

type request struct {
	Connection   connection      `json:"connection"`
	Runtime      runtimeEndpoint `json:"runtime"`
	ConnectionID string          `json:"connectionId"`
	Form         queryForm       `json:"form"`
	// mcp/call
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Lifecycle *lifecycle      `json:"lifecycle"`
}

type lifecycle struct {
	Connection connection      `json:"connection"`
	Runtime    runtimeEndpoint `json:"runtime"`
}

type plugin struct {
	mu       sync.RWMutex
	sessions map[string]*session
	now      func() time.Time
}

func newPlugin() *plugin {
	return &plugin{sessions: make(map[string]*session), now: time.Now}
}

func (p *plugin) Handle(_ sdk.RequestContext, method string, raw json.RawMessage, _ *sdk.Emitter) (any, *sdk.PluginError) {
	var input request
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, sdk.NewError(-32602, "Invalid request parameters")
		}
	}
	switch method {
	case "mcp/tools":
		return mcpTools(), nil
	case "mcp/call":
		return p.mcpCall(input), nil
	}
	result, err := p.handle(method, input)
	if err != nil {
		var unknown unknownMethodError
		if errors.As(err, &unknown) {
			return nil, sdk.MethodNotFound(method)
		}
		return nil, sdk.NewError(-32000, err.Error())
	}
	return result, nil
}

type unknownMethodError struct{}

func (unknownMethodError) Error() string { return "unknown method" }

func (p *plugin) handle(method string, input request) (any, error) {
	switch method {
	case "connection/test":
		s, err := newSession(input.Connection, input.Runtime)
		if err != nil {
			return nil, err
		}
		defer s.close()
		version, err := s.ping(p.now())
		if err != nil {
			return nil, err
		}
		message := "Connected to Loki"
		if version != "" {
			message += " " + version
		}
		return map[string]any{"success": true, "message": message}, nil
	case "connection/connect":
		if input.Connection.ID == "" {
			return nil, errors.New("missing connection id")
		}
		s, err := newSession(input.Connection, input.Runtime)
		if err != nil {
			return nil, err
		}
		if _, err := s.ping(p.now()); err != nil {
			s.close()
			return nil, err
		}
		p.store(input.Connection.ID, s)
		return map[string]any{"success": true}, nil
	case "connection/disconnect":
		if input.Connection.ID == "" {
			return nil, errors.New("missing connection id")
		}
		p.store(input.Connection.ID, nil)
		return map[string]any{"success": true}, nil
	}

	operation, ok := operations[method]
	if !ok {
		return nil, unknownMethodError{}
	}
	p.mu.RLock()
	s := p.sessions[input.ConnectionID]
	p.mu.RUnlock()
	if s == nil {
		return nil, errors.New("the Loki connection is not open; reopen it from the DBX sidebar")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, errors.New("the Loki connection was closed")
	}
	return operation(s, input.Form, p.now())
}

// store replaces the session for a connection id; nil removes it.
func (p *plugin) store(id string, s *session) {
	p.mu.Lock()
	old := p.sessions[id]
	if s == nil {
		delete(p.sessions, id)
	} else {
		p.sessions[id] = s
	}
	p.mu.Unlock()
	if old != nil {
		old.close()
	}
}

type operation func(*session, queryForm, time.Time) (any, error)

var operations = map[string]operation{
	"loki/info": func(s *session, _ queryForm, _ time.Time) (any, error) {
		info, err := s.buildInfo()
		if err != nil {
			// Build info is optional behind gateways; still describe the session.
			info = map[string]any{}
		}
		return map[string]any{"name": s.name, "baseUrl": s.base, "tenant": s.tenant, "build": info}, nil
	},
	"loki/labels": func(s *session, form queryForm, now time.Time) (any, error) {
		names, err := s.labels(form, now)
		if err != nil {
			return nil, err
		}
		return map[string]any{"labels": names}, nil
	},
	"loki/label_values": func(s *session, form queryForm, now time.Time) (any, error) {
		return s.labelValues(form, now)
	},
	"loki/query_range": func(s *session, form queryForm, now time.Time) (any, error) {
		return s.queryRange(form, now)
	},
	"loki/query": func(s *session, form queryForm, now time.Time) (any, error) {
		return s.queryInstant(form, now)
	},
	"loki/histogram": func(s *session, form queryForm, now time.Time) (any, error) {
		return s.histogram(form, now)
	},
	"loki/tail": func(s *session, form queryForm, now time.Time) (any, error) {
		return s.tail(form, now)
	},
}

func main() {
	server := sdk.NewServer(sdk.Metadata{ID: pluginID, Version: version, Capabilities: []string{"connections"}}, newPlugin())
	if err := server.Serve(); err != nil {
		log.Fatal(err)
	}
}
