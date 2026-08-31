package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/ardasevinc/herdr-codex-bridge/internal/bridge"
	"github.com/ardasevinc/herdr-codex-bridge/internal/herdr"
	"github.com/ardasevinc/herdr-codex-bridge/internal/protocol"
)

type HookInput struct {
	SessionID     string `json:"session_id"`
	HookEventName string `json:"hook_event_name"`
	Source        string `json:"source"`
}

type hookOutput struct {
	SystemMessage      string             `json:"systemMessage,omitempty"`
	SuppressOutput     bool               `json:"suppressOutput,omitempty"`
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

type hookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext,omitempty"`
}

func RunHook(ctx context.Context, action string, in io.Reader, out io.Writer, client *herdr.Client, keyPath string, now time.Time) error {
	var input HookInput
	if err := json.NewDecoder(io.LimitReader(in, 1<<20)).Decode(&input); err != nil {
		return fmt.Errorf("decode hook input: %w", err)
	}
	if input.SessionID == "" {
		input.SessionID = os.Getenv("CODEX_THREAD_ID")
	}
	if input.SessionID == "" {
		return nil
	}
	switch action {
	case "session-start":
		return sessionStart(ctx, input, out, client, keyPath, now)
	case "user-prompt-submit":
		return userPromptSubmit(ctx, input, out, client)
	default:
		return fmt.Errorf("unknown hook action %q", action)
	}
}

func sessionStart(ctx context.Context, input HookInput, out io.Writer, client *herdr.Client, keyPath string, now time.Time) error {
	source := input.Source
	if source == "" {
		source = "startup"
	}
	if os.Getenv("HERDR_ENV") == "1" && os.Getenv("HERDR_PANE_ID") != "" {
		if err := client.ReportSession(ctx, os.Getenv("HERDR_PANE_ID"), input.SessionID, source, uint64(now.UnixNano())); err != nil {
			return err
		}
		return writeContext(out, "SessionStart", nativeContext(input.SessionID, os.Getenv("HERDR_WORKSPACE_ID"), os.Getenv("HERDR_TAB_ID"), os.Getenv("HERDR_PANE_ID")))
	}
	if source == "compact" {
		association, err := bridge.Resolve(ctx, client, input.SessionID)
		if err != nil {
			return writeContext(out, "SessionStart", pendingContext(input.SessionID))
		}
		return writeContext(out, "SessionStart", associationContext(association))
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("read bridge key: %w", err)
	}
	marker, err := protocol.New(input.SessionID, source, now)
	if err != nil {
		return err
	}
	line, err := marker.Sign(key)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(hookOutput{
		SystemMessage: line,
		HookSpecificOutput: hookSpecificOutput{
			HookEventName:     "SessionStart",
			AdditionalContext: pendingContext(input.SessionID),
		},
	})
}

func userPromptSubmit(ctx context.Context, input HookInput, out io.Writer, client *herdr.Client) error {
	deadline := time.Now().Add(750 * time.Millisecond)
	for {
		association, err := bridge.Resolve(ctx, client, input.SessionID)
		if err == nil {
			return writeContext(out, "UserPromptSubmit", associationContext(association))
		}
		if errors.Is(err, bridge.ErrAmbiguous) {
			return writeContext(out, "UserPromptSubmit", "Herdr bridge found multiple live panes for this Codex thread. Caller-relative herdr-self actions are unsafe until the duplicate mapping is resolved; explicit Herdr targets remain available.")
		}
		if time.Now().After(deadline) {
			return writeContext(out, "UserPromptSubmit", pendingContext(input.SessionID))
		}
		time.Sleep(75 * time.Millisecond)
	}
}

func writeContext(out io.Writer, event, additionalContext string) error {
	return json.NewEncoder(out).Encode(hookOutput{
		SuppressOutput:     true,
		HookSpecificOutput: hookSpecificOutput{HookEventName: event, AdditionalContext: additionalContext},
	})
}

func pendingContext(threadID string) string {
	return "Herdr Codex Bridge knows this Codex thread as " + threadID + ". Its live pane association may still be pending. Use herdr-self for caller-relative Herdr actions; missing HERDR_ENV alone is expected with a centralized Codex app-server and is not a reason to refuse global or explicitly targeted Herdr operations."
}

func nativeContext(threadID, workspaceID, tabID, paneID string) string {
	return fmt.Sprintf("Herdr Codex Bridge associated Codex thread %s with workspace %s, tab %s, pane %s. Use herdr-self for caller-relative Herdr actions.", threadID, workspaceID, tabID, paneID)
}

func associationContext(a bridge.Association) string {
	return nativeContext(a.ThreadID, a.WorkspaceID, a.TabID, a.PaneID)
}
