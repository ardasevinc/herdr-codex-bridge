package bridge

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ardasevinc/herdr-codex-bridge/internal/herdr"
)

var (
	ErrUnmapped  = errors.New("Codex thread is not mapped to a live Herdr pane")
	ErrAmbiguous = errors.New("Codex thread maps to more than one live Herdr pane")
)

type AmbiguousError struct {
	Matches []Association
}

func (e *AmbiguousError) Error() string {
	panes := make([]string, 0, len(e.Matches))
	for _, match := range e.Matches {
		panes = append(panes, match.PaneID)
	}
	return fmt.Sprintf("%v: %s", ErrAmbiguous, strings.Join(panes, ", "))
}

func (e *AmbiguousError) Unwrap() error { return ErrAmbiguous }

type Association struct {
	ThreadID    string `json:"thread_id"`
	WorkspaceID string `json:"workspace_id"`
	TabID       string `json:"tab_id"`
	PaneID      string `json:"pane_id"`
	Source      string `json:"source"`
}

func Resolve(ctx context.Context, client *herdr.Client, threadID string) (Association, error) {
	agents, err := client.Agents(ctx)
	if err != nil {
		return Association{}, err
	}
	var matches []Association
	for _, agent := range agents {
		if agent.Agent != "codex" || agent.AgentSession == nil || agent.AgentSession.Kind != "id" || agent.AgentSession.Value != threadID {
			continue
		}
		matches = append(matches, Association{
			ThreadID: threadID, WorkspaceID: agent.WorkspaceID, TabID: agent.TabID,
			PaneID: agent.PaneID, Source: agent.AgentSession.Source,
		})
	}
	switch len(matches) {
	case 0:
		return Association{}, ErrUnmapped
	case 1:
		return matches[0], nil
	default:
		return Association{}, &AmbiguousError{Matches: matches}
	}
}
