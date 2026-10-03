package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type HostStats struct {
	OS           string  `json:"os"`
	Cpus         int     `json:"cpus"`
	MemoryGb     float64 `json:"memoryGb"`
	MemoryUsedGb float64 `json:"memoryUsedGb"`
	Load         float64 `json:"load"`
	DiskFreeGb   float64 `json:"diskFreeGb"`
	Battery      *int    `json:"battery"`
	Charging     *bool   `json:"charging"`
	Tart         string  `json:"tart"`
}

type ImageState struct {
	Ref      string `json:"ref"`
	Present  bool   `json:"present"`
	Pulling  bool   `json:"pulling"`
	Progress string `json:"progress"`
	Error    string `json:"error"`
}

type SlotReport struct {
	Pool   string     `json:"pool"`
	VM     string     `json:"vm"`
	State  string     `json:"state"`
	Runner string     `json:"runner"`
	Since  *time.Time `json:"since"`
	Error  string     `json:"error"`
}

type Report struct {
	Version string       `json:"version"`
	Host    HostStats    `json:"host"`
	Image   ImageState   `json:"image"`
	Slots   []SlotReport `json:"slots"`
	Error   string       `json:"error"`
}

type SlotPlan struct {
	Pool        string `json:"pool"`
	CPU         int    `json:"cpu"`
	MemoryGb    int    `json:"memoryGb"`
	Start       bool   `json:"start"`
	Keep        bool   `json:"keep"`
	IdleMinutes int    `json:"idleMinutes"`
}

type Plan struct {
	Paused    bool       `json:"paused"`
	Image     string     `json:"image"`
	CacheGb   int        `json:"cacheGb"`
	MinFreeGb int        `json:"minFreeGb"`
	Slots     []SlotPlan `json:"slots"`
}

type Jit struct {
	Name   string `json:"name"`
	Config string `json:"config"`
}

type Client struct {
	base  string
	token string
	http  *http.Client
	agent string
}

func New(base, token, version string) *Client {
	return &Client{
		base:  strings.TrimRight(base, "/"),
		token: token,
		http:  &http.Client{Timeout: 20 * time.Second},
		agent: "reekeer-agent/" + version,
	}
}

func (c *Client) post(ctx context.Context, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.agent)
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("panel %s: %w", path, err)
	}
	if res.StatusCode >= 300 {
		var detail struct {
			Detail string `json:"detail"`
		}
		if json.Unmarshal(raw, &detail) == nil && detail.Detail != "" {
			return fmt.Errorf("panel %s: %s", path, detail.Detail)
		}
		return fmt.Errorf("panel %s: HTTP %d", path, res.StatusCode)
	}
	if out == nil {
		return nil
	}
	if len(raw) == 0 {
		return fmt.Errorf("panel %s: empty response", path)
	}
	return json.Unmarshal(raw, out)
}

func (c *Client) Sync(ctx context.Context, r Report) (Plan, error) {
	var p Plan
	err := c.post(ctx, "/api/agent/sync", r, &p)
	return p, err
}

func (c *Client) JIT(ctx context.Context, pool, vm string) (Jit, error) {
	var j Jit
	err := c.post(ctx, "/api/agent/jit", map[string]string{"pool": pool, "vm": vm}, &j)
	return j, err
}

func (c *Client) Release(ctx context.Context, pool, name string) error {
	return c.post(ctx, "/api/agent/release", map[string]string{"pool": pool, "name": name}, nil)
}
