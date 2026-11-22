// Package clashapi is a small client for the Clash-compatible controller
// that sing-box (experimental.clash_api) and mihomo both serve.
package clashapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	base   string
	secret string
	http   *http.Client
}

func New(port int, secret string) *Client {
	return &Client{
		base:   fmt.Sprintf("http://127.0.0.1:%d", port),
		secret: secret,
		http:   &http.Client{Timeout: 3 * time.Second},
	}
}

func (c *Client) req(ctx context.Context, path string) (*http.Request, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+c.secret)
	return r, nil
}

type Version struct {
	Version string `json:"version"`
	Premium bool   `json:"premium"`
}

func (c *Client) Version(ctx context.Context) (Version, error) {
	var v Version
	r, err := c.req(ctx, "/version")
	if err != nil {
		return v, err
	}
	resp, err := c.http.Do(r)
	if err != nil {
		return v, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return v, fmt.Errorf("version: %s", resp.Status)
	}
	return v, json.NewDecoder(resp.Body).Decode(&v)
}

// Traffic is bytes per second, up and down.
type Traffic struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

// StreamTraffic calls fn for each one-second sample until ctx ends or the
// stream breaks. It holds one idle connection and no timer of its own.
func (c *Client) StreamTraffic(ctx context.Context, fn func(Traffic)) error {
	r, err := c.req(ctx, "/traffic")
	if err != nil {
		return err
	}
	// The stream never ends on its own: no overall timeout.
	resp, err := (&http.Client{}).Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("traffic: %s", resp.Status)
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		var t Traffic
		if json.Unmarshal(sc.Bytes(), &t) == nil {
			fn(t)
		}
	}
	return sc.Err()
}

// Proxy is one entry of /proxies: a node, or a group with its choices.
type Proxy struct {
	Type    string   `json:"type"`
	Now     string   `json:"now"` // a group's current choice
	All     []string `json:"all"`
	History []struct {
		Delay int `json:"delay"`
	} `json:"history"`
}

// LastDelay is the latest test result in ms, 0 when there is none or it failed.
func (p Proxy) LastDelay() int {
	if n := len(p.History); n > 0 {
		return p.History[n-1].Delay
	}
	return 0
}

func (c *Client) Proxies(ctx context.Context) (map[string]Proxy, error) {
	r, err := c.req(ctx, "/proxies")
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("proxies: %s", resp.Status)
	}
	var out struct {
		Proxies map[string]Proxy `json:"proxies"`
	}
	return out.Proxies, json.NewDecoder(resp.Body).Decode(&out)
}

// Select makes group use the named choice.
func (c *Client) Select(ctx context.Context, group, name string) error {
	body, _ := json.Marshal(map[string]string{"name": name})
	r, err := http.NewRequestWithContext(ctx, http.MethodPut, c.base+"/proxies/"+url.PathEscape(group), bytes.NewReader(body))
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+c.secret)
	r.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("select: %s", resp.Status)
	}
	return nil
}

// Delay tests one node and returns the round trip in ms.
func (c *Client) Delay(ctx context.Context, name, probeURL string) (int, error) {
	q := url.Values{"url": {probeURL}, "timeout": {"5000"}}
	r, err := c.req(ctx, "/proxies/"+url.PathEscape(name)+"/delay?"+q.Encode())
	if err != nil {
		return 0, err
	}
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(r)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var out struct {
		Delay   int    `json:"delay"`
		Message string `json:"message"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("delay: %s", out.Message)
	}
	return out.Delay, nil
}

// Conn is one live connection through the core.
type Conn struct {
	ID       string
	Host     string // the name asked for, or the address when there was none
	DestIP   string
	Port     string
	Network  string // tcp or udp
	Process  string // the program, when the core could tell
	Rule     string // what matched, in the core's words: "domain_suffix=a.com => route(x)"
	Payload  string
	Chain    []string // from the exit back to the group asked for
	Upload   int64
	Download int64
	Start    time.Time
}

// Connections lists what is open now.
func (c *Client) Connections(ctx context.Context) ([]Conn, error) {
	r, err := c.req(ctx, "/connections")
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("connections: %s", resp.Status)
	}
	var raw struct {
		Connections []struct {
			ID       string `json:"id"`
			Metadata struct {
				Network string `json:"network"`
				DestIP  string `json:"destinationIP"`
				Port    any    `json:"destinationPort"`
				Host    string `json:"host"`
				Process string `json:"processPath"`
			} `json:"metadata"`
			Upload      int64     `json:"upload"`
			Download    int64     `json:"download"`
			Start       time.Time `json:"start"`
			Chains      []string  `json:"chains"`
			Rule        string    `json:"rule"`
			RulePayload string    `json:"rulePayload"`
		} `json:"connections"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]Conn, 0, len(raw.Connections))
	for _, k := range raw.Connections {
		host := k.Metadata.Host
		if host == "" {
			host = k.Metadata.DestIP
		}
		proc := k.Metadata.Process
		if i := strings.LastIndex(proc, "/"); i >= 0 {
			proc = proc[i+1:]
		}
		out = append(out, Conn{
			ID: k.ID, Host: host, DestIP: k.Metadata.DestIP, Port: fmt.Sprint(k.Metadata.Port), Network: k.Metadata.Network,
			Process: proc, Rule: k.Rule, Payload: k.RulePayload, Chain: k.Chains, Upload: k.Upload, Download: k.Download, Start: k.Start,
		})
	}
	return out, nil
}

// CloseConnection ends one connection.
func (c *Client) CloseConnection(ctx context.Context, id string) error {
	r, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.base+"/connections/"+url.PathEscape(id), nil)
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+c.secret)
	resp, err := c.http.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("close: %s", resp.Status)
	}
	return nil
}
