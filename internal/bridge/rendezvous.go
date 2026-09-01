package bridge

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ardasevinc/herdr-codex-bridge/internal/protocol"
)

const (
	rendezvousDirectory = "rendezvous-v1"
	armLifetime         = 12 * time.Hour
	abandonedLifetime   = 15 * time.Minute
	claimLifetime       = 5 * time.Minute
	gateLifetime        = 5 * time.Minute
	mappedRefresh       = time.Hour
	sweepInterval       = 10 * time.Minute
	sweepLeaseLifetime  = time.Minute
	maxRendezvousLanes  = 512
	maxClaimsPerLane    = 8

	armPhaseArmed     = "armed"
	armPhaseAbandoned = "abandoned"
	armPhaseMapped    = "mapped"
)

type RendezvousStore struct {
	root string
	key  []byte
}

type WitnessClaim struct {
	PaneID     string
	Generation string
}

type armRecord struct {
	Version     int    `json:"version"`
	Generation  string `json:"generation"`
	Phase       string `json:"phase"`
	ArmedAt     int64  `json:"armed_at"`
	RefreshedAt int64  `json:"refreshed_at"`
	ExpiresAt   int64  `json:"expires_at"`
}

type claimRecord struct {
	Version     int    `json:"version"`
	Generation  string `json:"generation"`
	PaneID      string `json:"pane_id"`
	WitnessedAt int64  `json:"witnessed_at"`
	ExpiresAt   int64  `json:"expires_at"`
}

type sweepRecord struct {
	Version     int   `json:"version"`
	CompletedAt int64 `json:"completed_at"`
}

func NewRendezvousStore(keyPath string, key []byte) (*RendezvousStore, error) {
	if len(key) < 32 {
		return nil, errors.New("bridge key must contain at least 32 bytes")
	}
	store := &RendezvousStore{
		root: filepath.Join(filepath.Dir(keyPath), rendezvousDirectory),
		key:  append([]byte(nil), key...),
	}
	if err := ensurePrivateDirectory(store.root); err != nil {
		return nil, err
	}
	return store, nil
}

func RendezvousPath(keyPath string) string {
	return filepath.Join(filepath.Dir(keyPath), rendezvousDirectory)
}

func (store *RendezvousStore) Arm(marker protocol.Marker, now time.Time) error {
	lane, err := store.lane(marker.SessionID, true)
	if err != nil {
		return err
	}
	if err := clearTransientState(lane); err != nil {
		return err
	}
	record := armRecord{
		Version: 1, Generation: store.generation(marker), Phase: armPhaseArmed,
		ArmedAt: now.UTC().UnixMilli(), RefreshedAt: now.UTC().UnixMilli(),
		ExpiresAt: now.Add(armLifetime).UTC().UnixMilli(),
	}
	if err := writePrivateJSON(filepath.Join(lane, "arm.json"), record); err != nil {
		return err
	}
	return store.Sweep(now)
}

// Witness records pane-local evidence only when marker is the currently armed
// SessionStart generation. The claim clock starts here, not when Codex started.
func (store *RendezvousStore) Witness(marker protocol.Marker, paneID string, now time.Time) (bool, error) {
	if paneID == "" {
		return false, errors.New("witness pane id is empty")
	}
	lane, arm, err := store.activeArm(marker.SessionID, now)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if arm.Generation != store.generation(marker) {
		return false, nil
	}
	record := claimRecord{
		Version: 1, Generation: arm.Generation, PaneID: paneID,
		WitnessedAt: now.UTC().UnixMilli(), ExpiresAt: now.Add(claimLifetime).UTC().UnixMilli(),
	}
	name := "claim-" + store.token("pane", paneID) + ".json"
	if err := writePrivateJSON(filepath.Join(lane, name), record); err != nil {
		return false, err
	}
	if err := pruneLaneFiles(lane, "claim-", maxClaimsPerLane); err != nil {
		return false, err
	}
	return true, nil
}

func (store *RendezvousStore) Claims(sessionID string, now time.Time) ([]WitnessClaim, error) {
	lane, arm, err := store.activeArm(sessionID, now)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	entries, err := os.ReadDir(lane)
	if err != nil {
		return nil, err
	}
	claims := make([]WitnessClaim, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "claim-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		claimPath := filepath.Join(lane, entry.Name())
		var record claimRecord
		if err := readPrivateJSON(claimPath, &record); err != nil || !validClaim(record, arm, now) {
			_ = os.Remove(claimPath)
			continue
		}
		claims = append(claims, WitnessClaim{PaneID: record.PaneID, Generation: record.Generation})
	}
	sort.Slice(claims, func(i, j int) bool { return claims[i].PaneID < claims[j].PaneID })
	return claims, nil
}

// BeginWait makes the recovery grace period a bounded per-lifecycle cost. An
// abandoned non-Herdr lane expires shortly after its first silent prompt.
func (store *RendezvousStore) BeginWait(sessionID string, now time.Time) (bool, error) {
	_, arm, err := store.activeArm(sessionID, now)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return store.claimGate(sessionID, arm.Generation, "wait-", now)
}

func (store *RendezvousStore) MarkAbandoned(sessionID string, now time.Time) error {
	lane, arm, err := store.activeArm(sessionID, now)
	if err != nil {
		return err
	}
	if arm.Phase == armPhaseMapped || arm.Phase == armPhaseAbandoned {
		return nil
	}
	arm.Phase = armPhaseAbandoned
	arm.RefreshedAt = now.UTC().UnixMilli()
	arm.ExpiresAt = now.Add(abandonedLifetime).UTC().UnixMilli()
	return writePrivateJSON(filepath.Join(lane, "arm.json"), arm)
}

// MarkMapped compacts a lane to its recovery arm. Refreshes are rate-limited so
// active mapped sessions retain recovery without writing on every prompt.
func (store *RendezvousStore) MarkMapped(sessionID, generation string, now time.Time) error {
	lane, arm, err := store.activeArm(sessionID, now)
	if err != nil {
		return err
	}
	if generation != "" && generation != arm.Generation {
		return nil
	}
	if err := clearTransientState(lane); err != nil {
		return err
	}
	if arm.Phase == armPhaseMapped && now.Before(time.UnixMilli(arm.RefreshedAt).Add(mappedRefresh)) {
		return nil
	}
	arm.Phase = armPhaseMapped
	arm.RefreshedAt = now.UTC().UnixMilli()
	arm.ExpiresAt = now.Add(armLifetime).UTC().UnixMilli()
	return writePrivateJSON(filepath.Join(lane, "arm.json"), arm)
}

func (store *RendezvousStore) ClaimEmission(sessionID, generation string, now time.Time) (bool, error) {
	return store.claimGate(sessionID, generation, "emitted-", now)
}

func (store *RendezvousStore) ClaimAmbiguityNotice(sessionID, generation string, now time.Time) (bool, error) {
	return store.claimGate(sessionID, generation, "ambiguity-", now)
}

func (store *RendezvousStore) claimGate(sessionID, generation, prefix string, now time.Time) (bool, error) {
	lane, arm, err := store.activeArm(sessionID, now)
	if err != nil {
		return false, err
	}
	if generation == "" || generation != arm.Generation {
		return false, nil
	}
	gatePath := filepath.Join(lane, prefix+generation)
	for attempt := 0; attempt < 2; attempt++ {
		file, openErr := os.OpenFile(gatePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if openErr == nil {
			return true, file.Close()
		}
		if !errors.Is(openErr, os.ErrExist) {
			return false, openErr
		}
		info, statErr := os.Lstat(gatePath)
		if statErr != nil {
			continue
		}
		if now.Before(info.ModTime().Add(gateLifetime)) {
			return false, nil
		}
		if err := os.Remove(gatePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	return false, nil
}

// Sweep physically removes expired or invalid records. Expiry is also enforced
// on every read, so a sleeping machine cannot extend authorization lifetime.
func (store *RendezvousStore) Sweep(now time.Time) error {
	release, acquired, err := store.acquireSweepLease(now)
	if err != nil || !acquired {
		return err
	}
	defer release()
	var previous sweepRecord
	if err := readPrivateJSON(filepath.Join(store.root, ".last-sweep.json"), &previous); err == nil &&
		previous.Version == 1 && now.Before(time.UnixMilli(previous.CompletedAt).Add(sweepInterval)) {
		return nil
	}
	entries, err := os.ReadDir(store.root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "session-") {
			continue
		}
		lane := filepath.Join(store.root, entry.Name())
		arm, armErr := readArm(lane)
		if armErr != nil || expired(arm.ExpiresAt, now) {
			if err := os.RemoveAll(lane); err != nil {
				return err
			}
			continue
		}
		if err := sweepLane(lane, arm, now); err != nil {
			return err
		}
	}
	if err := pruneLanes(store.root, maxRendezvousLanes); err != nil {
		return err
	}
	return writePrivateJSON(filepath.Join(store.root, ".last-sweep.json"), sweepRecord{Version: 1, CompletedAt: now.UTC().UnixMilli()})
}

func (store *RendezvousStore) acquireSweepLease(now time.Time) (func(), bool, error) {
	lockPath := filepath.Join(store.root, ".sweep-lock")
	for attempt := 0; attempt < 2; attempt++ {
		file, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			if closeErr := file.Close(); closeErr != nil {
				_ = os.Remove(lockPath)
				return func() {}, false, closeErr
			}
			return func() { _ = os.Remove(lockPath) }, true, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return func() {}, false, err
		}
		info, statErr := os.Lstat(lockPath)
		if statErr != nil {
			continue
		}
		if now.Before(info.ModTime().Add(sweepLeaseLifetime)) {
			return func() {}, false, nil
		}
		if err := os.Remove(lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return func() {}, false, err
		}
	}
	return func() {}, false, nil
}

func (store *RendezvousStore) activeArm(sessionID string, now time.Time) (string, armRecord, error) {
	lane, err := store.lane(sessionID, false)
	if err != nil {
		return "", armRecord{}, err
	}
	arm, err := readArm(lane)
	if err != nil {
		return "", armRecord{}, err
	}
	if expired(arm.ExpiresAt, now) {
		_ = os.RemoveAll(lane)
		return "", armRecord{}, os.ErrNotExist
	}
	return lane, arm, nil
}

func (store *RendezvousStore) lane(sessionID string, create bool) (string, error) {
	if sessionID == "" {
		return "", errors.New("session id is empty")
	}
	lane := filepath.Join(store.root, "session-"+store.token("session", sessionID))
	if create {
		if err := ensurePrivateDirectory(lane); err != nil {
			return "", err
		}
		return lane, nil
	}
	info, err := os.Lstat(lane)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("rendezvous lane is not a private directory")
	}
	return lane, nil
}

func (store *RendezvousStore) generation(marker protocol.Marker) string {
	value := fmt.Sprintf("%s\x00%s\x00%d\x00%s", marker.SessionID, marker.Source, marker.IssuedAt.UnixMilli(), marker.Nonce)
	return store.token("generation", value)
}

func (store *RendezvousStore) token(domain, value string) string {
	mac := hmac.New(sha256.New, store.key)
	_, _ = mac.Write([]byte("herdr-codex-bridge:" + domain + "\x00" + value))
	return hex.EncodeToString(mac.Sum(nil))
}

func readArm(lane string) (armRecord, error) {
	var record armRecord
	if err := readPrivateJSON(filepath.Join(lane, "arm.json"), &record); err != nil {
		return armRecord{}, err
	}
	if record.Version != 1 || record.Generation == "" || record.ExpiresAt == 0 ||
		(record.Phase != armPhaseArmed && record.Phase != armPhaseAbandoned && record.Phase != armPhaseMapped) {
		return armRecord{}, errors.New("invalid rendezvous arm")
	}
	return record, nil
}

func validClaim(record claimRecord, arm armRecord, now time.Time) bool {
	return record.Version == 1 && record.Generation == arm.Generation && record.PaneID != "" && !expired(record.ExpiresAt, now)
}

func expired(expiresAt int64, now time.Time) bool {
	return expiresAt == 0 || now.UTC().UnixMilli() >= expiresAt
}

func sweepLane(lane string, arm armRecord, now time.Time) error {
	entries, err := os.ReadDir(lane)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return os.RemoveAll(lane)
		}
		path := filepath.Join(lane, entry.Name())
		if entry.Name() == "arm.json" {
			continue
		}
		if strings.HasPrefix(entry.Name(), "claim-") && strings.HasSuffix(entry.Name(), ".json") {
			var claim claimRecord
			if err := readPrivateJSON(path, &claim); err != nil || !validClaim(claim, arm, now) {
				_ = os.Remove(path)
			}
			continue
		}
		if strings.HasPrefix(entry.Name(), "wait-") || strings.HasPrefix(entry.Name(), "emitted-") || strings.HasPrefix(entry.Name(), "ambiguity-") {
			info, infoErr := entry.Info()
			if infoErr != nil || !now.Before(info.ModTime().Add(gateLifetime)) {
				_ = os.Remove(path)
			}
			continue
		}
		_ = os.Remove(path)
	}
	return nil
}

func clearTransientState(lane string) error {
	entries, err := os.ReadDir(lane)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "claim-") || strings.HasPrefix(name, "wait-") || strings.HasPrefix(name, "emitted-") || strings.HasPrefix(name, "ambiguity-") {
			if err := os.Remove(filepath.Join(lane, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func pruneLanes(root string, maximum int) error {
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) <= maximum {
		return err
	}
	type candidate struct {
		name    string
		modTime time.Time
	}
	lanes := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "session-") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr == nil {
			lanes = append(lanes, candidate{name: entry.Name(), modTime: info.ModTime()})
		}
	}
	sort.Slice(lanes, func(i, j int) bool { return lanes[i].modTime.Before(lanes[j].modTime) })
	for len(lanes) > maximum {
		if err := os.RemoveAll(filepath.Join(root, lanes[0].name)); err != nil {
			return err
		}
		lanes = lanes[1:]
	}
	return nil
}

func pruneLaneFiles(lane, prefix string, maximum int) error {
	entries, err := os.ReadDir(lane)
	if err != nil {
		return err
	}
	type candidate struct {
		name    string
		modTime time.Time
	}
	files := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr == nil {
			files = append(files, candidate{name: entry.Name(), modTime: info.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })
	for len(files) > maximum {
		if err := os.Remove(filepath.Join(lane, files[0].name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		files = files[1:]
	}
	return nil
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("rendezvous path is not a directory")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("rendezvous directory permissions are too broad")
	}
	return nil
}

func readPrivateJSON(path string, value any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 16<<10 {
		return errors.New("rendezvous record is not a small private regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func writePrivateJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".rendezvous-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
