package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"time"
)

type Client struct {
	SocketPath string
	sequence   atomic.Uint64
}

type request struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
}

type response struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type AgentSession struct {
	Source string `json:"source"`
	Agent  string `json:"agent"`
	Kind   string `json:"kind"`
	Value  string `json:"value"`
}

type Agent struct {
	Agent        string        `json:"agent"`
	AgentStatus  string        `json:"agent_status"`
	WorkspaceID  string        `json:"workspace_id"`
	TabID        string        `json:"tab_id"`
	PaneID       string        `json:"pane_id"`
	Revision     uint64        `json:"revision"`
	AgentSession *AgentSession `json:"agent_session"`
}

type Pane struct {
	Agent        string        `json:"agent"`
	AgentStatus  string        `json:"agent_status"`
	WorkspaceID  string        `json:"workspace_id"`
	TabID        string        `json:"tab_id"`
	PaneID       string        `json:"pane_id"`
	Revision     uint64        `json:"revision"`
	AgentSession *AgentSession `json:"agent_session"`
}

type PaneRead struct {
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
	TabID       string `json:"tab_id"`
	Text        string `json:"text"`
	Revision    uint64 `json:"revision"`
	Truncated   bool   `json:"truncated"`
}

type Plugin struct {
	PluginID   string `json:"plugin_id"`
	Version    string `json:"version"`
	Enabled    bool   `json:"enabled"`
	PluginRoot string `json:"plugin_root"`
}

func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	if c.SocketPath == "" {
		return errors.New("Herdr socket path is empty")
	}
	dialer := net.Dialer{Timeout: time.Second}
	conn, err := dialer.DialContext(ctx, "unix", c.SocketPath)
	if err != nil {
		return fmt.Errorf("connect to Herdr: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	id := fmt.Sprintf("herdr-self:%d:%d", time.Now().UnixMilli(), c.sequence.Add(1))
	if err := json.NewEncoder(conn).Encode(request{ID: id, Method: method, Params: params}); err != nil {
		return fmt.Errorf("send Herdr request: %w", err)
	}
	var envelope response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&envelope); err != nil {
		return fmt.Errorf("read Herdr response: %w", err)
	}
	if envelope.Error != nil {
		return fmt.Errorf("Herdr %s: %s", envelope.Error.Code, envelope.Error.Message)
	}
	if result != nil && len(envelope.Result) > 0 {
		if err := json.Unmarshal(envelope.Result, result); err != nil {
			return fmt.Errorf("decode Herdr response: %w", err)
		}
	}
	return nil
}

func (c *Client) Ping(ctx context.Context) error {
	return c.Call(ctx, "ping", map[string]any{}, nil)
}

func (c *Client) Agents(ctx context.Context) ([]Agent, error) {
	var result struct {
		Agents []Agent `json:"agents"`
	}
	err := c.Call(ctx, "agent.list", map[string]any{}, &result)
	return result.Agents, err
}

func (c *Client) Panes(ctx context.Context) ([]Pane, error) {
	var result struct {
		Panes []Pane `json:"panes"`
	}
	err := c.Call(ctx, "pane.list", map[string]any{}, &result)
	return result.Panes, err
}

func (c *Client) ReadPane(ctx context.Context, paneID string, lines uint32) (PaneRead, error) {
	var result struct {
		Read PaneRead `json:"read"`
	}
	err := c.Call(ctx, "pane.read", map[string]any{
		"pane_id": paneID,
		"source":  "recent",
		"lines":   lines,
		"format":  "text",
	}, &result)
	return result.Read, err
}

func (c *Client) Plugins(ctx context.Context) ([]Plugin, error) {
	var result struct {
		Plugins []Plugin `json:"plugins"`
	}
	err := c.Call(ctx, "plugin.list", map[string]any{}, &result)
	return result.Plugins, err
}

func (c *Client) ReportSession(ctx context.Context, paneID, sessionID, source string, sequence uint64) error {
	return c.Call(ctx, "pane.report_agent_session", map[string]any{
		"pane_id":              paneID,
		"source":               "herdr:codex",
		"agent":                "codex",
		"seq":                  sequence,
		"agent_session_id":     sessionID,
		"session_start_source": source,
	}, nil)
}
