package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRendezvousState(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if err := validateRendezvousState(missing); err != nil {
		t.Fatalf("missing state: %v", err)
	}
	root := filepath.Join(t.TempDir(), "rendezvous-v1")
	lane := filepath.Join(root, "session-token")
	if err := os.MkdirAll(lane, 0o700); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(lane, "arm.json")
	if err := os.WriteFile(record, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateRendezvousState(root); err != nil {
		t.Fatalf("private state: %v", err)
	}
	if err := os.Chmod(record, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateRendezvousState(root); err == nil {
		t.Fatal("broad record permissions passed validation")
	}
}

func TestDoctorReportIncludesBuildMetadataAndUnknownTrust(t *testing.T) {
	report := newDoctorReport()
	addUnknown(&report, "codex_hook_trust", "not queryable")
	if report.BridgeVersion == "" || report.Commit == "" || report.BuildDate == "" {
		t.Fatalf("missing build metadata: %#v", report)
	}
	if !report.OK || len(report.Checks) != 1 || report.Checks[0].Status != "unknown" {
		t.Fatalf("unknown check changed doctor health: %#v", report)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"bridge_version"`, `"commit"`, `"build_date"`} {
		if !strings.Contains(string(data), field) {
			t.Fatalf("doctor JSON missing %s: %s", field, data)
		}
	}
}
