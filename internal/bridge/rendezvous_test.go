package bridge

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ardasevinc/herdr-codex-bridge/internal/protocol"
)

func TestWitnessLifetimeStartsWhenPaneObservesMarker(t *testing.T) {
	store, key := testRendezvousStore(t)
	startedAt := time.Now().UTC().Add(-5 * time.Hour)
	marker, err := protocol.New("idle-thread", "startup", startedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Arm(marker, startedAt); err != nil {
		t.Fatal(err)
	}
	witnessedAt := startedAt.Add(5 * time.Hour)
	if witnessed, err := store.Witness(marker, "w3:p5", witnessedAt); err != nil || !witnessed {
		t.Fatalf("witness = %t, %v", witnessed, err)
	}
	claims, err := store.Claims(marker.SessionID, witnessedAt.Add(claimLifetime-time.Millisecond))
	if err != nil || len(claims) != 1 || claims[0].PaneID != "w3:p5" {
		t.Fatalf("live claims = %#v, %v", claims, err)
	}
	claims, err = store.Claims(marker.SessionID, witnessedAt.Add(claimLifetime+time.Millisecond))
	if err != nil || len(claims) != 0 {
		t.Fatalf("expired claims = %#v, %v", claims, err)
	}
	if bytes.Contains(readRendezvousFiles(t, store.root), []byte(marker.SessionID)) || bytes.Contains(readRendezvousFiles(t, store.root), []byte(marker.Nonce)) {
		t.Fatal("rendezvous state persisted raw session or marker identity")
	}
	_ = key
}

func TestWitnessRejectsPreviousLifecycleGeneration(t *testing.T) {
	store, _ := testRendezvousStore(t)
	now := time.Now().UTC()
	oldMarker, _ := protocol.New("same-thread", "startup", now)
	newMarker, _ := protocol.New("same-thread", "resume", now.Add(time.Second))
	if err := store.Arm(oldMarker, now); err != nil {
		t.Fatal(err)
	}
	if err := store.Arm(newMarker, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if witnessed, err := store.Witness(oldMarker, "w1:p1", now.Add(2*time.Second)); err != nil || witnessed {
		t.Fatalf("old generation witness = %t, %v", witnessed, err)
	}
	if witnessed, err := store.Witness(newMarker, "w1:p1", now.Add(2*time.Second)); err != nil || !witnessed {
		t.Fatalf("current generation witness = %t, %v", witnessed, err)
	}
}

func TestRecoveryEmissionGateAllowsOneConcurrentWinner(t *testing.T) {
	store, _ := testRendezvousStore(t)
	now := time.Now().UTC()
	marker, _ := protocol.New("racing-thread", "clear", now)
	if err := store.Arm(marker, now); err != nil {
		t.Fatal(err)
	}
	if witnessed, err := store.Witness(marker, "w2:p4", now); err != nil || !witnessed {
		t.Fatalf("witness = %t, %v", witnessed, err)
	}
	claims, err := store.Claims(marker.SessionID, now)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims = %#v, %v", claims, err)
	}
	var winners atomic.Int32
	var group sync.WaitGroup
	for range 16 {
		group.Add(1)
		go func() {
			defer group.Done()
			won, gateErr := store.ClaimEmission(marker.SessionID, claims[0].Generation, now)
			if gateErr != nil {
				t.Errorf("gate: %v", gateErr)
				return
			}
			if won {
				winners.Add(1)
			}
		}()
	}
	group.Wait()
	if winners.Load() != 1 {
		t.Fatalf("emission winners = %d", winners.Load())
	}
}

func TestWaitGateIsOncePerLifecycleAndRearms(t *testing.T) {
	store, _ := testRendezvousStore(t)
	now := time.Now().UTC()
	first, _ := protocol.New("quiet-thread", "startup", now)
	if err := store.Arm(first, now); err != nil {
		t.Fatal(err)
	}
	if wait, err := store.BeginWait(first.SessionID, now); err != nil || !wait {
		t.Fatalf("first wait = %t, %v", wait, err)
	}
	if wait, err := store.BeginWait(first.SessionID, now); err != nil || wait {
		t.Fatalf("repeat wait = %t, %v", wait, err)
	}
	second, _ := protocol.New("quiet-thread", "resume", now.Add(time.Second))
	if err := store.Arm(second, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if wait, err := store.BeginWait(second.SessionID, now.Add(time.Second)); err != nil || !wait {
		t.Fatalf("rearmed wait = %t, %v", wait, err)
	}
}

func TestArmExpiresAfterHardLifetime(t *testing.T) {
	store, _ := testRendezvousStore(t)
	now := time.Now().UTC()
	marker, _ := protocol.New("expired-thread", "startup", now)
	if err := store.Arm(marker, now); err != nil {
		t.Fatal(err)
	}
	if claims, err := store.Claims(marker.SessionID, now.Add(armLifetime)); err != nil || len(claims) != 0 {
		t.Fatalf("expired lane claims = %#v, %v", claims, err)
	}
	lane, _ := store.lane(marker.SessionID, false)
	if _, err := os.Stat(lane); !os.IsNotExist(err) {
		t.Fatalf("expired lane survived logical read: %v", err)
	}
}

func TestAbandonedLaneExpiresSoonAfterFirstPrompt(t *testing.T) {
	store, _ := testRendezvousStore(t)
	now := time.Now().UTC()
	marker, _ := protocol.New("non-herdr-thread", "startup", now)
	if err := store.Arm(marker, now); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAbandoned(marker.SessionID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	lane, arm, err := store.activeArm(marker.SessionID, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if arm.Phase != armPhaseAbandoned || arm.ExpiresAt != now.Add(time.Minute+abandonedLifetime).UnixMilli() {
		t.Fatalf("abandoned arm = %#v", arm)
	}
	if _, _, err := store.activeArm(marker.SessionID, now.Add(time.Minute+abandonedLifetime)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired abandoned arm = %v", err)
	}
	if _, err := os.Stat(lane); !os.IsNotExist(err) {
		t.Fatalf("abandoned lane survived expiry: %v", err)
	}
}

func TestMappedLaneCompactsAndRefreshesAtMostHourly(t *testing.T) {
	store, _ := testRendezvousStore(t)
	now := time.Now().UTC()
	marker, _ := protocol.New("mapped-thread", "startup", now)
	if err := store.Arm(marker, now); err != nil {
		t.Fatal(err)
	}
	if witnessed, err := store.Witness(marker, "w1:p1", now); err != nil || !witnessed {
		t.Fatalf("witness = %t, %v", witnessed, err)
	}
	if _, err := store.BeginWait(marker.SessionID, now); err != nil {
		t.Fatal(err)
	}
	generation := store.generation(marker)
	if _, err := store.ClaimEmission(marker.SessionID, generation, now); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkMapped(marker.SessionID, generation, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	lane, arm, err := store.activeArm(marker.SessionID, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(lane)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "arm.json" || arm.Phase != armPhaseMapped {
		t.Fatalf("mapped lane entries=%v arm=%#v", entries, arm)
	}
	originalRefresh := arm.RefreshedAt
	if err := store.MarkMapped(marker.SessionID, generation, now.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	_, arm, _ = store.activeArm(marker.SessionID, now.Add(30*time.Minute))
	if arm.RefreshedAt != originalRefresh {
		t.Fatalf("mapped arm refreshed too early: %d != %d", arm.RefreshedAt, originalRefresh)
	}
	if err := store.MarkMapped(marker.SessionID, generation, now.Add(61*time.Minute)); err != nil {
		t.Fatal(err)
	}
	_, arm, _ = store.activeArm(marker.SessionID, now.Add(61*time.Minute))
	if arm.RefreshedAt != now.Add(61*time.Minute).UnixMilli() || arm.ExpiresAt != now.Add(61*time.Minute+armLifetime).UnixMilli() {
		t.Fatalf("mapped arm was not refreshed: %#v", arm)
	}
}

func TestSweepRemovesExpiredAndInvalidLanes(t *testing.T) {
	store, _ := testRendezvousStore(t)
	now := time.Now().UTC()
	for _, sessionID := range []string{"expired", "valid"} {
		marker, _ := protocol.New(sessionID, "startup", now)
		if err := store.Arm(marker, now); err != nil {
			t.Fatal(err)
		}
	}
	invalid := filepath.Join(store.root, "session-invalid")
	if err := os.Mkdir(invalid, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(invalid, "arm.json"), []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(store.root, ".last-sweep.json")); err != nil {
		t.Fatal(err)
	}
	if err := store.Sweep(now.Add(armLifetime)); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(store.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "session-") {
			t.Fatalf("stale lane survived sweep: %s", entry.Name())
		}
	}
}

func TestRendezvousStateUsesPrivatePermissions(t *testing.T) {
	store, _ := testRendezvousStore(t)
	now := time.Now().UTC()
	marker, _ := protocol.New("private-thread", "startup", now)
	if err := store.Arm(marker, now); err != nil {
		t.Fatal(err)
	}
	if witnessed, err := store.Witness(marker, "w1:p1", now); err != nil || !witnessed {
		t.Fatalf("witness = %t, %v", witnessed, err)
	}
	err := filepath.WalkDir(store.root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		wanted := os.FileMode(0o600)
		if entry.IsDir() {
			wanted = 0o700
		}
		if info.Mode().Perm() != wanted {
			t.Errorf("%s permissions = %04o, want %04o", path, info.Mode().Perm(), wanted)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRendezvousStatePrunesOldLanes(t *testing.T) {
	store, _ := testRendezvousStore(t)
	for index := 0; index < 3; index++ {
		if err := os.Mkdir(filepath.Join(store.root, "session-"+strconv.Itoa(index)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := pruneLanes(store.root, 2); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(store.root)
	if err != nil {
		t.Fatal(err)
	}
	lanes := 0
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "session-") {
			lanes++
		}
	}
	if lanes != 2 {
		t.Fatalf("rendezvous lanes = %d, want 2", lanes)
	}
}

func TestRendezvousStateBoundsClaimsPerLane(t *testing.T) {
	store, _ := testRendezvousStore(t)
	now := time.Now().UTC()
	marker, _ := protocol.New("many-panes-thread", "startup", now)
	if err := store.Arm(marker, now); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxClaimsPerLane+1; index++ {
		witnessed, err := store.Witness(marker, "w1:p"+strconv.Itoa(index), now.Add(time.Duration(index)*time.Millisecond))
		if err != nil || !witnessed {
			t.Fatalf("witness %d = %t, %v", index, witnessed, err)
		}
	}
	claims, err := store.Claims(marker.SessionID, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != maxClaimsPerLane {
		t.Fatalf("claims = %d, want %d", len(claims), maxClaimsPerLane)
	}
}

func testRendezvousStore(t *testing.T) (*RendezvousStore, []byte) {
	t.Helper()
	key := bytes.Repeat([]byte{0x71}, 32)
	keyPath := filepath.Join(t.TempDir(), "bridge.key")
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewRendezvousStore(keyPath, key)
	if err != nil {
		t.Fatal(err)
	}
	return store, key
}

func readRendezvousFiles(t *testing.T, root string) []byte {
	t.Helper()
	var joined strings.Builder
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		joined.Write(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return []byte(joined.String())
}
