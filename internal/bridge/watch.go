package bridge

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ardasevinc/herdr-codex-bridge/internal/herdr"
	"github.com/ardasevinc/herdr-codex-bridge/internal/protocol"
)

const (
	watchInterval = 150 * time.Millisecond
)

func WatchPane(ctx context.Context, client *herdr.Client, paneID, keyPath string, startedAt time.Time, timeout time.Duration) error {
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("read bridge key: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()
	var lastRevision uint64
	var nextLivenessCheck time.Time
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil
			}
			return ctx.Err()
		case now := <-ticker.C:
			if !now.Before(nextLivenessCheck) {
				alive, _ := paneLiveState(ctx, client, paneID)
				if !alive {
					return nil
				}
				nextLivenessCheck = now.Add(2 * time.Second)
			}
			readCtx, readCancel := context.WithTimeout(ctx, time.Second)
			read, readErr := client.ReadPane(readCtx, paneID, 120)
			readCancel()
			if readErr != nil {
				if strings.Contains(readErr.Error(), "pane_not_found") {
					return nil
				}
				continue
			}
			if read.Revision == lastRevision {
				continue
			}
			lastRevision = read.Revision
			marker, ok := newestValidMarker(read.Text, key, now, startedAt)
			if !ok {
				continue
			}
			reportCtx, reportCancel := context.WithTimeout(ctx, time.Second)
			err := client.ReportSession(reportCtx, paneID, marker.SessionID, marker.Source, uint64(now.UnixNano()))
			reportCancel()
			if err != nil {
				return err
			}
			verifyCtx, verifyCancel := context.WithTimeout(ctx, time.Second)
			mapped := paneMappedTo(verifyCtx, client, paneID, marker.SessionID)
			verifyCancel()
			if mapped {
				return nil
			}
			return errors.New("Herdr accepted the session report but did not expose the expected pane mapping")
		}
	}
}

func paneMappedTo(ctx context.Context, client *herdr.Client, paneID, sessionID string) bool {
	panes, err := client.Panes(ctx)
	if err != nil {
		return false
	}
	for _, pane := range panes {
		if pane.PaneID == paneID {
			return pane.AgentSession != nil && pane.AgentSession.Agent == "codex" && pane.AgentSession.Value == sessionID
		}
	}
	return false
}

func paneLiveState(ctx context.Context, client *herdr.Client, paneID string) (alive, mapped bool) {
	checkCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	panes, err := client.Panes(checkCtx)
	if err != nil {
		return true, false
	}
	for _, pane := range panes {
		if pane.PaneID != paneID {
			continue
		}
		if pane.Agent != "codex" {
			return false, false
		}
		return true, pane.AgentSession != nil && pane.AgentSession.Agent == "codex"
	}
	return false, false
}

func newestValidMarker(text string, key []byte, now, startedAt time.Time) (protocol.Marker, bool) {
	var newest protocol.Marker
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, protocol.Prefix) {
			continue
		}
		marker, err := protocol.ParseAndVerify(line, key, now)
		if err != nil || marker.IssuedAt.Before(startedAt.Add(-2*time.Second)) {
			continue
		}
		if newest.IssuedAt.IsZero() || marker.IssuedAt.After(newest.IssuedAt) {
			newest = marker
		}
	}
	return newest, !newest.IssuedAt.IsZero()
}
