package app

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/ardasevinc/herdr-codex-bridge/internal/assets"
	"github.com/ardasevinc/herdr-codex-bridge/internal/version"
)

const docsSchema = "herdr-self/docs/v1"

type docsEnvelope struct {
	Schema   string           `json:"schema"`
	Version  string           `json:"version"`
	Topic    string           `json:"topic"`
	Markdown string           `json:"markdown"`
	Contract *commandContract `json:"contract,omitempty"`
}

type commandContract struct {
	Commands               []commandMetadata     `json:"commands"`
	SafeWithoutAssociation []safeCommandMetadata `json:"safe_without_association"`
	Rebind                 rebindMetadata        `json:"rebind"`
}

type commandMetadata struct {
	Name    string   `json:"name"`
	Forms   []string `json:"forms"`
	Effects []string `json:"effects"`
}

type safeCommandMetadata struct {
	Command     string   `json:"command"`
	Subcommands []string `json:"subcommands,omitempty"`
}

type rebindMetadata struct {
	IdentitySource        string   `json:"identity_source"`
	DefaultSocket         string   `json:"default_socket"`
	ExplicitSocketFlag    string   `json:"explicit_socket_flag"`
	PreviewEffects        []string `json:"preview_effects"`
	ApplyEffects          []string `json:"apply_effects"`
	Outcomes              []string `json:"outcomes"`
	PreviewFirst          bool     `json:"preview_first_workflow"`
	NonAtomic             bool     `json:"non_atomic"`
	MovesExistingThread   bool     `json:"moves_existing_thread"`
	OverridesDuplicates   bool     `json:"overrides_duplicate_mappings"`
	RetriesUncertain      bool     `json:"retries_uncertain"`
	RollsBackUncertain    bool     `json:"rolls_back_uncertain"`
	MaximumReportAttempts int      `json:"maximum_report_attempts"`
}

func (r Runtime) runDocs(args []string) error {
	topic := "index"
	topicSet := false
	jsonOutput := false
	for _, arg := range args {
		if arg == "--json" {
			if jsonOutput {
				return fmt.Errorf("--json may be supplied only once")
			}
			jsonOutput = true
			continue
		}
		if topicSet {
			return fmt.Errorf("docs accepts one topic; run herdr-self docs")
		}
		topic = arg
		topicSet = true
	}
	markdown, err := docsTopic(topic)
	if err != nil {
		return err
	}
	if !jsonOutput {
		_, err = fmt.Fprint(docsOutput(r.Stdout), markdown)
		return err
	}
	envelope := docsEnvelope{Schema: docsSchema, Version: version.Effective(), Topic: topic, Markdown: markdown}
	if topic == "commands" {
		contract := bridgeCommandContract()
		envelope.Contract = &contract
	}
	return json.NewEncoder(docsOutput(r.Stdout)).Encode(envelope)
}

func docsTopic(topic string) (string, error) {
	if topic == "agents" {
		return string(assets.CodexSkill), nil
	}
	if topic != "index" && topic != "commands" {
		return "", fmt.Errorf("unknown docs topic %q; run herdr-self docs", topic)
	}
	content, err := assets.Docs.ReadFile("docs/" + topic + ".md")
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func bridgeCommandContract() commandContract {
	safeCommands := make([]safeCommandMetadata, 0, len(safeWithoutAssociation))
	for _, group := range safeWithoutAssociation {
		safeCommands = append(safeCommands, safeCommandMetadata{Command: group.command, Subcommands: append([]string(nil), group.subcommands...)})
	}
	return commandContract{
		Commands: []commandMetadata{
			{Name: "self", Forms: []string{"herdr-self", "herdr-self --json"}, Effects: []string{"Herdr read"}},
			{Name: "doctor", Forms: []string{"herdr-self doctor", "herdr-self doctor --json"}, Effects: []string{"filesystem read", "Herdr read", "process metadata read"}},
			{Name: "rebind", Forms: []string{"herdr-self rebind --pane ID [--replace]", "herdr-self rebind --pane ID [--replace] --apply"}, Effects: []string{"preview: Herdr read", "apply: Herdr read, at most one session report, Herdr verification read"}},
			{Name: "setup", Forms: []string{"herdr-self setup codex [--apply] [--force]"}, Effects: []string{"preview: filesystem and Herdr inspection", "apply: filesystem writes, managed Herdr plugin installation, possible network access and plugin build command, hook/skill/key/install-state reconciliation"}},
			{Name: "teardown", Forms: []string{"herdr-self teardown codex [--apply] [--force]"}, Effects: []string{"preview: filesystem and Herdr inspection", "apply: filesystem writes, managed bridge removal, possible official Herdr integration restoration"}},
			{Name: "docs", Forms: []string{"herdr-self docs [agents|commands] [--json]"}, Effects: []string{"none"}},
			{Name: "help", Forms: []string{"herdr-self --help", "herdr-self --bridge-help"}, Effects: []string{"--bridge-help: none", "--help: upstream process execution"}},
			{Name: "skill", Forms: []string{"herdr-self --skill"}, Effects: []string{"upstream process execution"}},
			{Name: "version", Forms: []string{"herdr-self --version"}, Effects: []string{"none"}},
		},
		SafeWithoutAssociation: safeCommands,
		Rebind: rebindMetadata{
			IdentitySource:        "CODEX_THREAD_ID",
			DefaultSocket:         "canonical Herdr socket; inherited HERDR_SOCKET_PATH ignored",
			ExplicitSocketFlag:    "--socket PATH",
			PreviewEffects:        []string{"one Herdr pane list read"},
			ApplyEffects:          []string{"precheck Herdr pane list read", "at most one pane.report_agent_session attempt", "verification Herdr pane list read"},
			Outcomes:              []string{"preview", "observed", "already_correct", "rejected", "uncertain"},
			PreviewFirst:          true,
			NonAtomic:             true,
			MovesExistingThread:   false,
			OverridesDuplicates:   false,
			RetriesUncertain:      false,
			RollsBackUncertain:    false,
			MaximumReportAttempts: 1,
		},
	}
}

func docsOutput(writer io.Writer) io.Writer {
	if writer == nil {
		return io.Discard
	}
	return writer
}
