package codex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ardasevinc/herdr-codex-bridge/internal/herdr"
)

const manualRebindCommand = "herdr-rebind"

const manualRPCTimeout = 1500 * time.Millisecond

type ManualRebindRequest struct {
	PaneID  string
	Replace bool
	Apply   bool
}

type ManualRebindStatus string

const (
	ManualRebindPreview        ManualRebindStatus = "preview"
	ManualRebindObserved       ManualRebindStatus = "observed"
	ManualRebindAlreadyCorrect ManualRebindStatus = "already_correct"
	ManualRebindRejected       ManualRebindStatus = "rejected"
	ManualRebindUncertain      ManualRebindStatus = "uncertain"
)

type ManualRebindOutcome struct {
	Status  ManualRebindStatus
	Message string
}

func (outcome ManualRebindOutcome) Successful() bool {
	return outcome.Status == ManualRebindPreview || outcome.Status == ManualRebindObserved || outcome.Status == ManualRebindAlreadyCorrect
}

type manualRebindState struct {
	Existing       string
	AlreadyCorrect bool
}

func parseManualRebind(prompt string) (ManualRebindRequest, bool, error) {
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return ManualRebindRequest{}, false, nil
	}
	fields := strings.Fields(trimmed)
	if fields[0] != manualRebindCommand {
		return ManualRebindRequest{}, false, nil
	}
	if strings.ContainsAny(trimmed, "\r\n") {
		return ManualRebindRequest{}, true, errors.New("the command must occupy one line")
	}
	request, err := ParseManualRebindArgs(fields[1:])
	return request, true, err
}

func ParseManualRebindArgs(args []string) (ManualRebindRequest, error) {
	request := ManualRebindRequest{}
	seenPane := false
	seenReplace := false
	seenApply := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--pane":
			if seenPane {
				return ManualRebindRequest{}, errors.New("--pane may be supplied only once")
			}
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return ManualRebindRequest{}, errors.New("--pane requires a fully qualified pane ID")
			}
			request.PaneID = args[index+1]
			seenPane = true
			index++
		case "--replace":
			if seenReplace {
				return ManualRebindRequest{}, errors.New("--replace may be supplied only once")
			}
			request.Replace = true
			seenReplace = true
		case "--apply":
			if seenApply {
				return ManualRebindRequest{}, errors.New("--apply may be supplied only once")
			}
			request.Apply = true
			seenApply = true
		default:
			return ManualRebindRequest{}, fmt.Errorf("unknown argument %q", args[index])
		}
	}
	if request.PaneID == "" || !strings.Contains(request.PaneID, ":") {
		return ManualRebindRequest{}, errors.New("--pane requires a fully qualified pane ID")
	}
	return request, nil
}

func manualRebind(ctx context.Context, input HookInput, request ManualRebindRequest, out io.Writer, client *herdr.Client, now time.Time) error {
	if input.SessionID == "" {
		return writeBlocked(out, "Herdr manual rebind rejected: the UserPromptSubmit payload has no session_id")
	}
	if input.AgentID != "" || input.AgentType != "" {
		return writeBlocked(out, "Herdr manual rebind rejected: subagents cannot change pane associations")
	}
	outcome := RunManualRebind(ctx, input.SessionID, request, client, now)
	return writeBlocked(out, outcome.Message)
}

func RunManualRebind(ctx context.Context, sessionID string, request ManualRebindRequest, client *herdr.Client, now time.Time) ManualRebindOutcome {
	if sessionID == "" {
		return manualOutcome(ManualRebindRejected, "Herdr manual rebind rejected: CODEX_THREAD_ID is unset")
	}
	precheckCtx, cancelPrecheck := context.WithTimeout(ctx, manualRPCTimeout)
	state, err := inspectManualRebind(precheckCtx, client, request, sessionID)
	cancelPrecheck()
	if err != nil {
		return manualOutcome(ManualRebindRejected, "Herdr manual rebind rejected: "+err.Error())
	}
	details := fmt.Sprintf("socket=%s pane=%s thread=%s existing=%s", client.SocketPath, request.PaneID, sessionID, state.Existing)
	if state.AlreadyCorrect {
		return manualOutcome(ManualRebindAlreadyCorrect, "Herdr manual rebind is already correct; no report sent. "+details)
	}
	if !request.Apply {
		next := "--apply"
		if state.Existing != "unmapped" && !request.Replace {
			next = "--replace --apply"
		}
		return manualOutcome(ManualRebindPreview, "Herdr manual rebind preview; no report sent. "+details+"; rerun with "+next)
	}

	reportCtx, cancelReport := context.WithTimeout(ctx, manualRPCTimeout)
	err = client.ReportSession(reportCtx, request.PaneID, sessionID, recoverySessionStartSource, uint64(now.UnixNano()))
	cancelReport()
	if err != nil {
		var rpcError *herdr.RPCError
		if errors.As(err, &rpcError) {
			return manualOutcome(ManualRebindRejected, "Herdr declined the manual rebind: "+err.Error()+". "+details)
		}
		return manualOutcome(ManualRebindUncertain, "Herdr manual rebind outcome is uncertain; inspect the target before retrying: "+err.Error()+". "+details)
	}
	verifyCtx, cancelVerify := context.WithTimeout(ctx, manualRPCTimeout)
	verified, verifyErr := inspectManualRebind(verifyCtx, client, request, sessionID)
	cancelVerify()
	if verifyErr != nil || !verified.AlreadyCorrect {
		message := "Herdr accepted the manual report, but the outcome is uncertain; inspect the target before retrying. " + details
		if verifyErr != nil {
			message += "; verification=" + verifyErr.Error()
		}
		return manualOutcome(ManualRebindUncertain, message)
	}
	return manualOutcome(ManualRebindObserved, "Herdr manual rebind observed the requested unique mapping. "+details)
}

func manualOutcome(status ManualRebindStatus, message string) ManualRebindOutcome {
	return ManualRebindOutcome{Status: status, Message: message}
}

func inspectManualRebind(ctx context.Context, client *herdr.Client, request ManualRebindRequest, sessionID string) (manualRebindState, error) {
	panes, err := client.Panes(ctx)
	if err != nil {
		return manualRebindState{}, fmt.Errorf("inspect live panes: %w", err)
	}
	state := manualRebindState{Existing: "unmapped"}
	foundTarget := false
	requestedPanes := make([]string, 0, 2)
	for _, pane := range panes {
		if isRequestedSession(pane.AgentSession, sessionID) {
			requestedPanes = append(requestedPanes, pane.PaneID)
		}
		if pane.PaneID == request.PaneID {
			foundTarget = true
			state.Existing = formatAgentSession(pane.AgentSession)
			if pane.Agent != "codex" {
				return manualRebindState{}, fmt.Errorf("target pane %s does not contain Codex", request.PaneID)
			}
		}
	}
	if !foundTarget {
		return manualRebindState{}, fmt.Errorf("target pane %s does not exist", request.PaneID)
	}
	if len(requestedPanes) > 1 || len(requestedPanes) == 1 && requestedPanes[0] != request.PaneID {
		return manualRebindState{}, fmt.Errorf("requested thread is already mapped outside the target: %s", strings.Join(requestedPanes, ", "))
	}
	state.AlreadyCorrect = len(requestedPanes) == 1 && requestedPanes[0] == request.PaneID
	if state.AlreadyCorrect {
		return state, nil
	}
	if state.Existing != "unmapped" && !request.Replace {
		return manualRebindState{}, fmt.Errorf("target has a different mapping (%s); add --replace to acknowledge replacement", state.Existing)
	}
	return state, nil
}

func isRequestedSession(session *herdr.AgentSession, sessionID string) bool {
	return session != nil && session.Agent == "codex" && session.Kind == "id" && session.Value == sessionID
}

func formatAgentSession(session *herdr.AgentSession) string {
	if session == nil {
		return "unmapped"
	}
	return fmt.Sprintf("%s/%s/%s (%s)", session.Agent, session.Kind, session.Value, session.Source)
}
