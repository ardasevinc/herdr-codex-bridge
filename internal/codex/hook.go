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

// Herdr permits an existing Codex pane's session identity to be replaced only
// by recognized SessionStart sources. Prompt recovery exists for the in-process
// thread replacement caused by /clear, so it must retain that semantic rather
// than inventing a bridge-specific source that Herdr would acknowledge but
// ignore.
const recoverySessionStartSource = "clear"

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
		return userPromptSubmit(ctx, input, out, client, keyPath, now)
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
	if store, storeErr := bridge.NewRendezvousStore(keyPath, key); storeErr == nil {
		_ = store.Arm(marker, now)
	}
	return json.NewEncoder(out).Encode(hookOutput{
		SystemMessage: line,
		HookSpecificOutput: hookSpecificOutput{
			HookEventName:     "SessionStart",
			AdditionalContext: pendingContext(input.SessionID),
		},
	})
}

func userPromptSubmit(ctx context.Context, input HookInput, out io.Writer, client *herdr.Client, keyPath string, now time.Time) error {
	_, resolveErr := bridge.Resolve(ctx, client, input.SessionID)
	if resolveErr == nil {
		markMappedState(keyPath, input.SessionID, now)
		return nil
	}
	if errors.Is(resolveErr, bridge.ErrAmbiguous) {
		return writeContext(out, "UserPromptSubmit", "Herdr bridge found multiple live panes for this Codex thread. herdr-self refuses mutations until the duplicate mapping is resolved, but still permits its documented read-only commands. If an explicit mutation is necessary, call upstream herdr directly with a fully specified target.")
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil
	}
	store, err := bridge.NewRendezvousStore(keyPath, key)
	if err != nil {
		return nil
	}
	_ = store.Sweep(now)
	deadline := time.Time{}
	for {
		claims, claimErr := store.Claims(input.SessionID, now)
		if claimErr == nil && len(claims) > 0 {
			_, resolveErr = bridge.Resolve(ctx, client, input.SessionID)
			if resolveErr == nil {
				_ = store.MarkMapped(input.SessionID, "", now)
				return nil
			}
			if errors.Is(resolveErr, bridge.ErrAmbiguous) {
				return writeContext(out, "UserPromptSubmit", "Herdr bridge found multiple live panes for this Codex thread. herdr-self refuses mutations until the duplicate mapping is resolved, but still permits its documented read-only commands. If an explicit mutation is necessary, call upstream herdr directly with a fully specified target.")
			}
			liveClaims, liveErr := liveWitnessClaims(ctx, client, claims)
			if liveErr != nil {
				return nil
			}
			if len(liveClaims) > 1 {
				notice, noticeErr := store.ClaimAmbiguityNotice(input.SessionID, liveClaims[0].Generation, now)
				if noticeErr != nil || !notice {
					return nil
				}
				return writeContext(out, "UserPromptSubmit", "Herdr bridge found multiple panes claiming this Codex thread. It will not emit a recovery marker or permit caller-relative mutations until the duplicate claim is resolved.")
			}
			if len(liveClaims) == 1 {
				won, gateErr := store.ClaimEmission(input.SessionID, liveClaims[0].Generation, now)
				if gateErr != nil || !won {
					return nil
				}
				return writePendingMarker(out, "UserPromptSubmit", input.SessionID, recoverySessionStartSource, pendingContext(input.SessionID), key, now)
			}
		}
		if deadline.IsZero() {
			shouldWait, waitErr := store.BeginWait(input.SessionID, now)
			if waitErr != nil || !shouldWait {
				return nil
			}
			deadline = time.Now().Add(750 * time.Millisecond)
		}
		if time.Now().After(deadline) {
			_ = store.MarkAbandoned(input.SessionID, now)
			return nil
		}
		time.Sleep(75 * time.Millisecond)
	}
}

func markMappedState(keyPath, sessionID string, now time.Time) {
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return
	}
	store, err := bridge.NewRendezvousStore(keyPath, key)
	if err != nil {
		return
	}
	_ = store.Sweep(now)
	_ = store.MarkMapped(sessionID, "", now)
}

func liveWitnessClaims(ctx context.Context, client *herdr.Client, claims []bridge.WitnessClaim) ([]bridge.WitnessClaim, error) {
	panes, err := client.Panes(ctx)
	if err != nil {
		return nil, err
	}
	live := make([]bridge.WitnessClaim, 0, len(claims))
	for _, claim := range claims {
		for _, pane := range panes {
			if pane.PaneID == claim.PaneID && pane.Agent == "codex" {
				live = append(live, claim)
				break
			}
		}
	}
	return live, nil
}

func writePendingMarker(out io.Writer, event, sessionID, source, context string, key []byte, now time.Time) error {
	marker, err := protocol.New(sessionID, source, now)
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
			HookEventName: event, AdditionalContext: context,
		},
	})
}

func writeContext(out io.Writer, event, additionalContext string) error {
	return json.NewEncoder(out).Encode(hookOutput{
		SuppressOutput:     true,
		HookSpecificOutput: hookSpecificOutput{HookEventName: event, AdditionalContext: additionalContext},
	})
}

func pendingContext(threadID string) string {
	return "Herdr Codex Bridge knows this Codex thread as " + threadID + ". Its live pane association may still be pending. Missing HERDR_ENV alone is expected with a centralized Codex app-server. herdr-self still permits its documented read-only commands, but refuses mutations until mapping succeeds; call upstream herdr directly only when you have a fully specified explicit target."
}

func nativeContext(threadID, workspaceID, tabID, paneID string) string {
	return fmt.Sprintf("Herdr Codex Bridge associated Codex thread %s with workspace %s, tab %s, pane %s. Use herdr-self for caller-relative Herdr actions.", threadID, workspaceID, tabID, paneID)
}

func associationContext(a bridge.Association) string {
	return nativeContext(a.ThreadID, a.WorkspaceID, a.TabID, a.PaneID)
}
