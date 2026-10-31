package unifi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"

	"github.com/jgulick48/unifi-statsd-exporter/internal/config"
)

// Client talks to the UniFi OS Network API.
type Client struct {
	cfg  config.UniFi
	base string
	http *http.Client

	mu        sync.Mutex
	loggedIn  bool
	csrfToken string
}

// NewClient builds a client from configuration. It does not contact the console.
func NewClient(cfg config.UniFi) (*Client, error) {
	base := strings.TrimRight(cfg.URL, "/")
	if base == "" {
		return nil, errors.New("unifi: url is required")
	}
	if _, err := url.Parse(base); err != nil {
		return nil, fmt.Errorf("unifi: invalid url: %w", err)
	}
	if cfg.APIKey == "" && (cfg.Username == "" || cfg.Password == "") {
		return nil, errors.New("unifi: apiKey or username/password is required")
	}
	if cfg.Site == "" {
		cfg.Site = "default"
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &Client{
		cfg:  cfg,
		base: base,
		http: &http.Client{
			Timeout: cfg.Timeout,
			Jar:     jar,
			Transport: &http.Transport{
				Proxy:           http.ProxyFromEnvironment,
				TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify},
			},
		},
	}, nil
}

// Devices fetches all adopted devices on the configured site.
func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	path := "/proxy/network/api/s/" + url.PathEscape(c.cfg.Site) + "/stat/device"

	if c.cfg.APIKey == "" {
		if err := c.ensureLogin(ctx); err != nil {
			return nil, err
		}
	}
	body, status, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized && c.cfg.APIKey == "" {
		if err := c.login(ctx); err != nil {
			return nil, err
		}
		body, status, err = c.get(ctx, path)
		if err != nil {
			return nil, err
		}
	}
	if status < 200 || status > 299 {
		return nil, fmt.Errorf("unifi: GET %s: unexpected status %d", path, status)
	}
	var resp rawResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unifi: decoding device response: %w", err)
	}
	if resp.Meta.RC != "ok" {
		return nil, fmt.Errorf("unifi: api error: rc=%q msg=%q", resp.Meta.RC, resp.Meta.Msg)
	}
	devices := make([]Device, 0, len(resp.Data))
	for _, r := range resp.Data {
		if d, ok := r.toDevice(); ok {
			devices = append(devices, d)
		}
	}
	return devices, nil
}

func (c *Client) ensureLogin(ctx context.Context) error {
	c.mu.Lock()
	ok := c.loggedIn
	c.mu.Unlock()
	if ok {
		return nil
	}
	return c.login(ctx)
}

func (c *Client) login(ctx context.Context) error {
	payload, err := json.Marshal(map[string]any{
		"username": c.cfg.Username,
		"password": c.cfg.Password,
		"remember": true,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/auth/login", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("unifi: login request failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("unifi: login failed: status %d", resp.StatusCode)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if t := resp.Header.Get("X-Csrf-Token"); t != "" {
		c.csrfToken = t
	}
	c.loggedIn = true
	return nil
}

func (c *Client) get(ctx context.Context, path string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("X-API-KEY", c.cfg.APIKey)
	} else {
		c.mu.Lock()
		if c.csrfToken != "" {
			req.Header.Set("X-Csrf-Token", c.csrfToken)
		}
		c.mu.Unlock()
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("unifi: GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if t := resp.Header.Get("X-Updated-Csrf-Token"); t != "" {
		c.mu.Lock()
		c.csrfToken = t
		c.mu.Unlock()
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("unifi: reading response: %w", err)
	}
	return body, resp.StatusCode, nil
}
