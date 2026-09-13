package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ardasevinc/herdr-codex-bridge/internal/assets"
	"github.com/ardasevinc/herdr-codex-bridge/internal/codex"
)

func TestEmbeddedDocsNeedNoIdentityConfigurationHerdrOrUpstream(t *testing.T) {
	missingConfig := t.TempDir() + "/must-remain-absent"
	t.Setenv("XDG_CONFIG_HOME", missingConfig)
	for _, args := range [][]string{{"docs"}, {"docs", "agents"}, {"docs", "commands"}, {"docs", "agents", "--json"}, {"docs", "commands", "--json"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout bytes.Buffer
			delegated := false
			runtime := Runtime{
				Stdout:  &stdout,
				Stderr:  &bytes.Buffer{},
				Environ: []string{},
				Exec: func(_, _ []string) error {
					delegated = true
					return nil
				},
			}
			if err := runtime.Run(context.Background(), args); err != nil {
				t.Fatal(err)
			}
			if stdout.Len() < 100 || delegated {
				t.Fatalf("output bytes/delegated = %d/%t", stdout.Len(), delegated)
			}
			if _, err := os.Stat(missingConfig); !os.IsNotExist(err) {
				t.Fatalf("offline docs touched configuration path: %v", err)
			}
		})
	}
}

func TestDocsAgentsReturnsExactEmbeddedSkill(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		var stdout bytes.Buffer
		args := []string{"docs", "agents"}
		if jsonOutput {
			args = append(args, "--json")
		}
		runtime := Runtime{Stdout: &stdout, Stderr: &bytes.Buffer{}}
		if err := runtime.Run(context.Background(), args); err != nil {
			t.Fatal(err)
		}
		markdown := stdout.String()
		if jsonOutput {
			var envelope docsEnvelope
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Schema != docsSchema || envelope.Topic != "agents" || envelope.Contract != nil {
				t.Fatalf("unexpected envelope: %#v", envelope)
			}
			markdown = envelope.Markdown
		}
		if markdown != string(assets.CodexSkill) {
			t.Fatal("docs agents diverged from the embedded skill")
		}
	}
}

func TestDocsCommandsJSONMatchesLiveRegistriesAndRebindContract(t *testing.T) {
	var stdout bytes.Buffer
	runtime := Runtime{Stdout: &stdout, Stderr: &bytes.Buffer{}}
	if err := runtime.Run(context.Background(), []string{"docs", "commands", "--json"}); err != nil {
		t.Fatal(err)
	}
	var envelope docsEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Schema != docsSchema || envelope.Topic != "commands" || envelope.Version == "" || envelope.Contract == nil || !strings.Contains(envelope.Markdown, "Reassociate an unmapped thread") {
		t.Fatalf("unexpected envelope: %#v", envelope)
	}
	wantSafe := make([]safeCommandMetadata, 0, len(safeWithoutAssociation))
	for _, group := range safeWithoutAssociation {
		wantSafe = append(wantSafe, safeCommandMetadata{Command: group.command, Subcommands: append([]string(nil), group.subcommands...)})
	}
	if !reflect.DeepEqual(envelope.Contract.SafeWithoutAssociation, wantSafe) {
		t.Fatalf("safe metadata = %#v, want %#v", envelope.Contract.SafeWithoutAssociation, wantSafe)
	}
	wantOutcomes := []string{
		string(codex.ManualRebindPreview),
		string(codex.ManualRebindObserved),
		string(codex.ManualRebindAlreadyCorrect),
		string(codex.ManualRebindRejected),
		string(codex.ManualRebindUncertain),
	}
	rebind := envelope.Contract.Rebind
	if !reflect.DeepEqual(rebind.Outcomes, wantOutcomes) || rebind.IdentitySource != "CODEX_THREAD_ID" || rebind.MaximumReportAttempts != 1 || !rebind.PreviewFirst || !rebind.NonAtomic {
		t.Fatalf("rebind metadata = %#v", rebind)
	}
	if rebind.MovesExistingThread || rebind.OverridesDuplicates || rebind.RetriesUncertain || rebind.RollsBackUncertain {
		t.Fatalf("rebind metadata overstates effects: %#v", rebind)
	}
	setup := commandByName(t, envelope.Contract.Commands, "setup")
	if effects := strings.Join(setup.Effects, " "); !strings.Contains(effects, "network access") || !strings.Contains(effects, "plugin build command") || !strings.Contains(effects, "filesystem writes") {
		t.Fatalf("setup effects = %q", effects)
	}
	teardown := commandByName(t, envelope.Contract.Commands, "teardown")
	if effects := strings.Join(teardown.Effects, " "); !strings.Contains(effects, "possible official Herdr integration restoration") || !strings.Contains(effects, "filesystem writes") {
		t.Fatalf("teardown effects = %q", effects)
	}
}

func TestDocsRejectsUnknownAndMixedTopics(t *testing.T) {
	for _, args := range [][]string{{"docs", "unknown"}, {"docs", "index", "agents"}, {"docs", "index", "index"}, {"docs", "agents", "commands"}, {"docs", "--json", "agents", "commands"}, {"docs", "agents", "--json", "commands"}, {"docs", "commands", "--json", "--json"}, {"docs", "commands", "--socket", "/tmp/nope"}, {"--socket", "/tmp/nope", "docs", "commands"}, {"docs", "--socket", ""}, {"--socket", "", "docs"}, {"--socket", "/tmp/first", "--socket", "", "docs"}} {
		delegated := false
		runtime := Runtime{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Exec: func(_, _ []string) error { delegated = true; return nil }}
		if err := runtime.Run(context.Background(), args); err == nil {
			t.Fatalf("Run(%q) succeeded", args)
		}
		if delegated {
			t.Fatalf("Run(%q) delegated", args)
		}
	}
}

func TestDocsExplicitIndex(t *testing.T) {
	var implicit, explicit bytes.Buffer
	for output, args := range map[*bytes.Buffer][]string{&implicit: {"docs"}, &explicit: {"docs", "index"}} {
		runtime := Runtime{Stdout: output, Stderr: &bytes.Buffer{}}
		if err := runtime.Run(context.Background(), args); err != nil {
			t.Fatal(err)
		}
	}
	if implicit.String() != explicit.String() {
		t.Fatalf("explicit index differs:\nimplicit=%q\nexplicit=%q", implicit.String(), explicit.String())
	}
}

func commandByName(t *testing.T, commands []commandMetadata, name string) commandMetadata {
	t.Helper()
	for _, command := range commands {
		if command.Name == name {
			return command
		}
	}
	t.Fatalf("missing command metadata %q", name)
	return commandMetadata{}
}
