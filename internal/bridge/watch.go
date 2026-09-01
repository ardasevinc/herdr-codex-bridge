package bridge

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

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
	store, storeErr := NewRendezvousStore(keyPath, key)
	if storeErr == nil {
		_ = store.Sweep(startedAt)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()
	var cursor paneSnapshotCursor
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
			if !cursor.changed(read) {
				continue
			}
			marker, ok := newestValidMarker(read.Text, key, now, startedAt)
			if !ok {
				if storeErr == nil {
					for _, witnessed := range authenticatedMarkers(read.Text, key) {
						_, _ = store.Witness(witnessed, paneID, now)
					}
				}
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
				if storeErr == nil {
					_ = store.MarkMapped(marker.SessionID, store.generation(marker), now)
				}
				return nil
			}
			return errors.New("Herdr accepted the session report but did not expose the expected pane mapping")
		}
	}
}

type paneSnapshotCursor struct {
	revision     uint64
	haveRevision bool
	digest       [sha256.Size]byte
	haveDigest   bool
}

func (cursor *paneSnapshotCursor) changed(read herdr.PaneRead) bool {
	if read.Revision != 0 {
		changed := !cursor.haveRevision || read.Revision != cursor.revision
		cursor.revision = read.Revision
		cursor.haveRevision = true
		return changed
	}
	digest := sha256.Sum256([]byte(read.Text))
	changed := !cursor.haveDigest || digest != cursor.digest
	cursor.digest = digest
	cursor.haveDigest = true
	return changed
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
	return newestMarker(text, func(record string) (protocol.Marker, error) {
		return protocol.ParseAndVerify(record, key, now)
	}, func(marker protocol.Marker) bool {
		return !marker.IssuedAt.Before(startedAt.Add(-2 * time.Second))
	})
}

func newestAuthenticatedMarker(text string, key []byte) (protocol.Marker, bool) {
	markers := authenticatedMarkers(text, key)
	if len(markers) == 0 {
		return protocol.Marker{}, false
	}
	return markers[0], true
}

func authenticatedMarkers(text string, key []byte) []protocol.Marker {
	markers := matchingMarkers(text, func(record string) (protocol.Marker, error) {
		return protocol.ParseAndVerifySignature(record, key)
	}, func(protocol.Marker) bool { return true })
	sort.Slice(markers, func(i, j int) bool { return markers[i].IssuedAt.After(markers[j].IssuedAt) })
	if len(markers) > 32 {
		markers = markers[:32]
	}
	return markers
}

func newestMarker(text string, parse func(string) (protocol.Marker, error), accept func(protocol.Marker) bool) (protocol.Marker, bool) {
	markers := matchingMarkers(text, parse, accept)
	var newest protocol.Marker
	for _, marker := range markers {
		if newest.IssuedAt.IsZero() || marker.IssuedAt.After(newest.IssuedAt) {
			newest = marker
		}
	}
	return newest, !newest.IssuedAt.IsZero()
}

func matchingMarkers(text string, parse func(string) (protocol.Marker, error), accept func(protocol.Marker) bool) []protocol.Marker {
	var markers []protocol.Marker
	remaining := text
	for {
		start := strings.Index(remaining, protocol.Prefix)
		if start < 0 {
			break
		}
		candidate := remaining[start:]
		end := strings.IndexByte(candidate, ']')
		if end < 0 {
			break
		}
		record := candidate[:end+1]
		marker, err := parse(compactRenderedMarker(record))
		if err != nil {
			remaining = candidate[len(protocol.Prefix):]
			continue
		}
		remaining = candidate[end+1:]
		if !accept(marker) {
			continue
		}
		markers = append(markers, marker)
	}
	return markers
}

func compactRenderedMarker(record string) string {
	var compact strings.Builder
	compact.Grow(len(record))
	for _, char := range record {
		if !unicode.IsSpace(char) {
			compact.WriteRune(char)
		}
	}
	normalized := compact.String()
	for _, field := range []string{"session=", "source=", "issued_at=", "nonce=", "sig="} {
		normalized = strings.Replace(normalized, field, " "+field, 1)
	}
	return normalized
}
