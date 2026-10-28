package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// connection mirrors the subset of DBX's ConnectionConfig that the plugin
// receives in lifecycle payloads (connection/test, connection/connect and
// mcp/call). Secret-bound fields arrive resolved in connection_secrets.
type connection struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Host            string            `json:"host"`
	Port            int               `json:"port"`
	Username        string            `json:"username"`
	Password        string            `json:"password"`
	Config          config            `json:"external_config"`
	Secrets         map[string]string `json:"connection_secrets"`
	TransportLayers []transportLayer  `json:"transport_layers"`
}

type config struct {
	Scheme             string `json:"scheme"`
	ContextPath        string `json:"context_path"`
	AuthMode           string `json:"auth_mode"`
	Tenant             string `json:"tenant"`
	InsecureSkipVerify any    `json:"insecure_skip_verify"`
}

type transportLayer struct {
	Type    string `json:"type"`
	Enabled *bool  `json:"enabled"`
}

type runtimeEndpoint struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type session struct {
	mu        sync.RWMutex
	closed    bool
	base      string
	name      string
	tenant    string
	authMode  string
	username  string
	password  string
	token     string
	client    *http.Client
	transport *http.Transport
}

// upstreamLimit caps how much of a single Loki response is read. Results are
// trimmed further before they cross the 8 MiB sidecar message limit.
const upstreamLimit = 16 << 20

const errorBodyLimit = 2048

func newSession(c connection, runtime runtimeEndpoint) (*session, error) {
	scheme := strings.ToLower(strings.TrimSpace(c.Config.Scheme))
	if scheme == "" {
		scheme = "http"
	}
	if scheme != "http" && scheme != "https" {
		return nil, errors.New("only HTTP or HTTPS is supported")
	}
	host := strings.TrimSpace(c.Host)
	if host == "" || strings.ContainsAny(host, "/@?#\\% \t\r\n[]") || (strings.Contains(host, ":") && net.ParseIP(host) == nil) {
		return nil, errors.New("invalid host: enter a hostname or IP without scheme, port or path")
	}
	if c.Port < 1 || c.Port > 65535 {
		return nil, errors.New("port must be between 1 and 65535")
	}
	path := strings.TrimSpace(c.Config.ContextPath)
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "?#%\\ \t\r\n") {
		return nil, errors.New("path prefix must start with / and must not contain a query, fragment or encoding")
	}
	for _, part := range strings.Split(path, "/") {
		if part == "." || part == ".." {
			return nil, errors.New("path prefix must not contain . or ..")
		}
	}
	tenant := strings.TrimSpace(c.Config.Tenant)
	if strings.ContainsAny(tenant, "\r\n") || len(tenant) > 512 {
		return nil, errors.New("invalid tenant ID")
	}

	authMode := c.Config.AuthMode
	if authMode == "" {
		authMode = "none"
	}
	s := &session{name: c.Name, tenant: tenant, authMode: authMode}
	switch authMode {
	case "none":
	case "basic":
		if c.Username == "" || c.Password == "" {
			return nil, errors.New("basic auth requires both username and password")
		}
		s.username, s.password = c.Username, c.Password
	case "bearer":
		s.token = strings.TrimSpace(c.Secrets["token"])
		if s.token == "" || strings.ContainsAny(s.token, "\r\n") {
			return nil, errors.New("bearer auth requires a token")
		}
	default:
		return nil, errors.New("unsupported auth mode")
	}

	s.base = (&url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(c.Port)), Path: strings.TrimRight(path, "/")}).String()
	s.transport = &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: truthy(c.Config.InsecureSkipVerify), ServerName: host},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		MaxIdleConnsPerHost:   4,
	}
	active := false
	for _, layer := range c.TransportLayers {
		if layer.Enabled != nil && !*layer.Enabled {
			continue
		}
		if layer.Type != "ssh" && layer.Type != "proxy" && layer.Type != "http_tunnel" {
			return nil, errors.New("unsupported DBX transport layer")
		}
		active = true
	}
	if active {
		if runtime.Host == "" || strings.ContainsAny(runtime.Host, "/@?#\\ \t\r\n") || runtime.Port < 1 || runtime.Port > 65535 {
			return nil, errors.New("DBX did not provide a tunnel endpoint; refusing to bypass the configured tunnel")
		}
		endpoint := net.JoinHostPort(runtime.Host, strconv.Itoa(runtime.Port))
		s.transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, endpoint)
		}
	}
	s.client = &http.Client{
		Transport:     s.transport,
		Timeout:       90 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return s, nil
}

func truthy(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1"
	}
	return false
}

func (s *session) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.transport.CloseIdleConnections()
}

// get performs a GET against the Loki HTTP API and returns the raw body.
// Loki reports query errors as plain text with a 4xx status, so the body is
// surfaced in the error to show LogQL parse errors to the user.
func (s *session) get(path string, values url.Values) ([]byte, error) {
	u := s.base + path
	if len(values) > 0 {
		u += "?" + values.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	switch s.authMode {
	case "basic":
		req.SetBasicAuth(s.username, s.password)
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	if s.tenant != "" {
		req.Header.Set("X-Scope-OrgID", s.tenant)
	}
	req.Header.Set("Accept", "application/json")
	res, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Loki request failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, errorBodyLimit))
		return nil, httpError(res.StatusCode, body)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, upstreamLimit+1))
	if err != nil {
		return nil, fmt.Errorf("Loki response read failed: %w", err)
	}
	if len(body) > upstreamLimit {
		return nil, fmt.Errorf("Loki response exceeds %d MiB; narrow the time range or lower the limit", upstreamLimit>>20)
	}
	return body, nil
}

func httpError(status int, body []byte) error {
	message := strings.TrimSpace(string(body))
	var payload struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil {
		if payload.Message != "" {
			message = payload.Message
		} else if payload.Error != "" {
			message = payload.Error
		}
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		if message == "" {
			message = "check the credentials and tenant ID"
		}
		return fmt.Errorf("Loki rejected the request (HTTP %d): %s", status, message)
	case status >= 300 && status < 400:
		return fmt.Errorf("Loki returned a redirect (HTTP %d); check the scheme and path prefix", status)
	case message == "":
		return fmt.Errorf("Loki returned HTTP %d", status)
	default:
		return fmt.Errorf("Loki returned HTTP %d: %s", status, message)
	}
}

// getData unwraps Loki's {"status":"success","data":...} envelope.
func (s *session) getData(path string, values url.Values, target any) error {
	body, err := s.get(path, values)
	if err != nil {
		return err
	}
	var envelope struct {
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
		Error  string          `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return errors.New("Loki response is not valid JSON; check the path prefix")
	}
	if envelope.Status != "success" {
		if envelope.Error != "" {
			return fmt.Errorf("Loki query failed: %s", envelope.Error)
		}
		return errors.New("Loki query failed")
	}
	if target == nil {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, target); err != nil {
		return errors.New("Loki returned an unexpected data shape")
	}
	return nil
}
