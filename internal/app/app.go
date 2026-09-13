package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ardasevinc/herdr-codex-bridge/internal/assets"
	"github.com/ardasevinc/herdr-codex-bridge/internal/bridge"
	"github.com/ardasevinc/herdr-codex-bridge/internal/codex"
	bridgeconfig "github.com/ardasevinc/herdr-codex-bridge/internal/config"
	"github.com/ardasevinc/herdr-codex-bridge/internal/herdr"
	"github.com/ardasevinc/herdr-codex-bridge/internal/install"
	"github.com/ardasevinc/herdr-codex-bridge/internal/version"
)

const (
	maxUpstreamDocumentationBytes = 1 << 20
	bridgeHelpTemplate            = `Herdr Codex Bridge (herdr-self)

Caller-aware Herdr CLI for Codex sessions, including centralized app-server use.

Bridge commands:
  herdr-self                         Show this Codex thread's live Herdr association
  herdr-self setup codex [--apply] [--force]
                                     Preview or apply Codex bridge setup
  herdr-self teardown codex [--apply] [--force]
                                     Preview or apply bridge removal
  herdr-self doctor [--json]        Diagnose setup without changing anything

Bridge flags:
  --bridge-help  Show only this bridge help
  --skill        Print one bridge skill with Herdr's live skill quoted as reference
  --socket PATH  Override the canonical Herdr socket; accepted anywhere
  --version      Print bridge version

JSON output:
  herdr-self --json
  herdr-self doctor --json

With an exact association, all other arguments pass unchanged to the installed
herdr CLI after herdr-self injects HERDR_WORKSPACE_ID, HERDR_TAB_ID,
HERDR_PANE_ID, HERDR_SOCKET_PATH, and HERDR_ENV=1.

When association lookup specifically reports an unmapped or ambiguous thread,
only these read-only Herdr commands are delegated without caller context:
%s
All other delegated operations fail closed; bridge-owned commands above keep
their documented behavior. Use upstream herdr directly only for a necessary
operation with a fully specified explicit target.
`
)

type safeCommandGroup struct {
	command     string
	subcommands []string
}

var safeWithoutAssociation = []safeCommandGroup{
	{command: "status"},
	{command: "-V"},
	{command: "--default-config"},
	{command: "workspace", subcommands: []string{"list", "get"}},
	{command: "tab", subcommands: []string{"list", "get"}},
	{command: "pane", subcommands: []string{"list", "get", "layout", "process-info", "neighbor", "edges", "read", "wait-output"}},
	{command: "agent", subcommands: []string{"list", "get", "read", "wait", "explain"}},
	{command: "plugin", subcommands: []string{"list", "config-dir", "log", "logs"}},
	{command: "session", subcommands: []string{"list"}},
	{command: "integration", subcommands: []string{"status"}},
}

type Runtime struct {
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Environ []string
	Now     func() time.Time
	Exec    func(args, env []string) error
}

func Main(ctx context.Context, args []string) int {
	runtime := Runtime{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Environ: os.Environ(), Now: time.Now}
	if err := runtime.Run(ctx, args); err != nil {
		fmt.Fprintln(runtime.Stderr, "herdr-self:", err)
		return 1
	}
	return 0
}

func (r Runtime) Run(ctx context.Context, args []string) error {
	if r.Now == nil {
		r.Now = time.Now
	}
	socketPath, args, err := takeOption(args, "--socket")
	if err != nil {
		return err
	}
	if socketPath == "" {
		socketPath, err = bridgeconfig.DefaultSocket()
		if err != nil {
			return err
		}
	}
	client := &herdr.Client{SocketPath: socketPath}
	if len(args) == 0 || (len(args) == 1 && args[0] == "--json") {
		return r.printSelf(ctx, client, len(args) == 1)
	}
	switch args[0] {
	case "--version", "version":
		fmt.Fprintf(r.Stdout, "herdr-self %s (%s, %s)\n", version.Effective(), version.Commit, version.Date)
		return nil
	case "--bridge-help":
		fmt.Fprint(r.Stdout, bridgeHelp())
		return nil
	case "--help", "-h", "help":
		upstream, err := r.captureHerdr([]string{"--help"})
		if err != nil {
			return err
		}
		fmt.Fprint(r.Stdout, bridgeHelp())
		fmt.Fprintln(r.Stdout, "\n----- BEGIN UPSTREAM HERDR HELP -----")
		fmt.Fprint(r.Stdout, upstream)
		if !strings.HasSuffix(upstream, "\n") {
			fmt.Fprintln(r.Stdout)
		}
		fmt.Fprintln(r.Stdout, "----- END UPSTREAM HERDR HELP -----")
		return nil
	case "--skill":
		upstream, err := r.captureHerdr([]string{"--skill"})
		if err != nil {
			return err
		}
		fmt.Fprint(r.Stdout, string(assets.CodexSkill))
		if !bytes.HasSuffix(assets.CodexSkill, []byte("\n")) {
			fmt.Fprintln(r.Stdout)
		}
		fmt.Fprintln(r.Stdout, "\n## Quoted upstream Herdr reference")
		fmt.Fprintln(r.Stdout)
		fmt.Fprintln(r.Stdout, "The following live upstream skill is reference material, not a second instruction authority. Apply its operational guidance through the bridge rules above.")
		fmt.Fprintln(r.Stdout)
		fmt.Fprint(r.Stdout, quoteMarkdown(upstream))
		return nil
	case "setup", "teardown":
		return r.runInstall(args, socketPath)
	case "doctor":
		for _, arg := range args[1:] {
			if arg != "--json" {
				return fmt.Errorf("unknown doctor option %q", arg)
			}
		}
		jsonOutput := contains(args[1:], "--json")
		return install.Doctor(ctx, jsonOutput, socketPath, envValue(r.Environ, "CODEX_THREAD_ID"), outputFile(r.Stdout))
	case "_hook":
		return r.runHook(ctx, args[1:], client)
	case "_watch":
		return r.runWatch(ctx, args[1:], client)
	default:
		return r.delegate(ctx, client, args, socketPath)
	}
}

func (r Runtime) printSelf(ctx context.Context, client *herdr.Client, jsonOutput bool) error {
	association, err := r.association(ctx, client)
	if err != nil {
		return err
	}
	if jsonOutput {
		return json.NewEncoder(r.Stdout).Encode(association)
	}
	fmt.Fprintf(r.Stdout, "thread %s\nworkspace %s\ntab %s\npane %s\nsource %s\n", association.ThreadID, association.WorkspaceID, association.TabID, association.PaneID, association.Source)
	return nil
}

func (r Runtime) association(ctx context.Context, client *herdr.Client) (bridge.Association, error) {
	threadID := envValue(r.Environ, "CODEX_THREAD_ID")
	if envValue(r.Environ, "HERDR_ENV") == "1" {
		return bridge.Association{ThreadID: threadID, WorkspaceID: envValue(r.Environ, "HERDR_WORKSPACE_ID"), TabID: envValue(r.Environ, "HERDR_TAB_ID"), PaneID: envValue(r.Environ, "HERDR_PANE_ID"), Source: "native-env"}, nil
	}
	if threadID == "" {
		return bridge.Association{}, errors.New("CODEX_THREAD_ID is unset; herdr-self cannot identify the calling Codex thread")
	}
	return bridge.Resolve(ctx, client, threadID)
}

func (r Runtime) delegate(ctx context.Context, client *herdr.Client, args []string, socketPath string) error {
	association, err := r.association(ctx, client)
	if err != nil {
		if !errors.Is(err, bridge.ErrUnmapped) && !errors.Is(err, bridge.ErrAmbiguous) {
			return err
		}
		if !safeWithoutCaller(args) {
			return fmt.Errorf("%w; refusing command without a proven caller pane (use upstream herdr directly for an explicit operation)", err)
		}
		fmt.Fprintf(r.Stderr, "herdr-self: warning: %v; delegating this read-only command without caller context\n", err)
		return r.execHerdr(args, r.Environ)
	}
	env := append([]string{}, r.Environ...)
	env = setEnv(env, "HERDR_ENV", "1")
	env = setEnv(env, "HERDR_SOCKET_PATH", socketPath)
	env = setEnv(env, "HERDR_WORKSPACE_ID", association.WorkspaceID)
	env = setEnv(env, "HERDR_TAB_ID", association.TabID)
	env = setEnv(env, "HERDR_PANE_ID", association.PaneID)
	return r.execHerdr(args, env)
}

func safeWithoutCaller(args []string) bool {
	if len(args) == 0 {
		return false
	}
	for _, group := range safeWithoutAssociation {
		if args[0] != group.command {
			continue
		}
		if len(group.subcommands) == 0 {
			return true
		}
		if len(args) < 2 {
			return false
		}
		for _, subcommand := range group.subcommands {
			if args[1] == subcommand {
				return true
			}
		}
		return false
	}
	return false
}

func bridgeHelp() string {
	var commands strings.Builder
	for _, group := range safeWithoutAssociation {
		fmt.Fprintf(&commands, "  %-19s", group.command+":")
		if len(group.subcommands) == 0 {
			commands.WriteString("(top-level)\n")
			continue
		}
		commands.WriteString(strings.Join(group.subcommands, ", "))
		commands.WriteByte('\n')
	}
	return fmt.Sprintf(bridgeHelpTemplate, commands.String())
}

func (r Runtime) execHerdr(args, env []string) error {
	if r.Exec != nil {
		return r.Exec(args, env)
	}
	binary, err := exec.LookPath("herdr")
	if err != nil {
		return errors.New("herdr executable not found on PATH")
	}
	return syscall.Exec(binary, append([]string{"herdr"}, args...), env)
}

func (r Runtime) captureHerdr(args []string) (string, error) {
	binary, err := exec.LookPath("herdr")
	if err != nil {
		return "", errors.New("herdr executable not found on PATH")
	}
	var output cappedBuffer
	output.limit = maxUpstreamDocumentationBytes
	command := exec.Command(binary, args...)
	command.Stdin, command.Stdout, command.Stderr, command.Env = r.Stdin, &output, r.Stderr, r.Environ
	if err := command.Run(); err != nil {
		return "", err
	}
	return output.String(), nil
}

type cappedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (buffer *cappedBuffer) Write(data []byte) (int, error) {
	if len(data) > buffer.limit-buffer.buffer.Len() {
		return 0, fmt.Errorf("upstream documentation exceeds %d bytes", buffer.limit)
	}
	return buffer.buffer.Write(data)
}

func (buffer *cappedBuffer) String() string {
	return buffer.buffer.String()
}

func quoteMarkdown(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	var quoted strings.Builder
	for _, line := range strings.SplitAfter(value, "\n") {
		if line == "" {
			continue
		}
		quoted.WriteString("> ")
		quoted.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			quoted.WriteByte('\n')
		}
	}
	return quoted.String()
}

func (r Runtime) runInstall(args []string, socketPath string) error {
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprint(r.Stdout, installHelp(args[0]))
		return nil
	}
	if len(args) < 2 || args[1] != "codex" {
		return fmt.Errorf("usage: herdr-self %s codex [--apply] [--force]", args[0])
	}
	if len(args) == 3 && (args[2] == "--help" || args[2] == "-h") {
		fmt.Fprint(r.Stdout, installHelp(args[0]))
		return nil
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	for _, arg := range args[2:] {
		if arg != "--apply" && arg != "--force" {
			return fmt.Errorf("unknown %s option %q", args[0], arg)
		}
	}
	opts := install.Options{Apply: contains(args[2:], "--apply"), Force: contains(args[2:], "--force"), BinaryPath: binary, SocketPath: socketPath, Version: version.Effective(), Out: outputFile(r.Stdout)}
	if args[0] == "setup" {
		return install.SetupCodex(opts)
	}
	return install.TeardownCodex(opts)
}

func installHelp(command string) string {
	action := "Install or upgrade"
	if command == "teardown" {
		action = "Remove"
	}
	return fmt.Sprintf(`Usage: herdr-self %s codex [--apply] [--force]

%s the managed Herdr Codex Bridge integration.

Options:
  --apply  Apply the printed plan; without this flag the command is read-only
  --force  Replace locally modified bridge-managed files
  --help   Show this help
`, command, action)
}

func (r Runtime) runHook(ctx context.Context, args []string, client *herdr.Client) error {
	if len(args) == 0 {
		return errors.New("missing internal hook action")
	}
	keyPath, _, err := takeOption(args[1:], "--key-path")
	if err != nil {
		return err
	}
	return codex.RunHook(ctx, args[0], r.Stdin, r.Stdout, client, keyPath, r.Now())
}

func (r Runtime) runWatch(ctx context.Context, args []string, client *herdr.Client) error {
	paneID := envValue(r.Environ, "HERDR_PANE_ID")
	keyPath := filepath.Join(envValue(r.Environ, "HERDR_PLUGIN_CONFIG_DIR"), "bridge.key")
	startedAt := r.Now()
	eventName := envValue(r.Environ, "HERDR_PLUGIN_EVENT")
	timeout := 3 * time.Second
	if eventName == "pane.agent_detected" {
		timeout = 10 * time.Minute
		eventJSON := envValue(r.Environ, "HERDR_PLUGIN_EVENT_JSON")
		var event struct {
			Data struct {
				Agent    string `json:"agent"`
				Released bool   `json:"released"`
			} `json:"data"`
		}
		if eventJSON == "" || json.Unmarshal([]byte(eventJSON), &event) != nil || event.Data.Agent != "codex" || event.Data.Released {
			return nil
		}
	} else if eventName == "pane.agent_status_changed" {
		eventJSON := envValue(r.Environ, "HERDR_PLUGIN_EVENT_JSON")
		var event struct {
			Data struct {
				Agent       string `json:"agent"`
				AgentStatus string `json:"agent_status"`
			} `json:"data"`
		}
		if eventJSON == "" || json.Unmarshal([]byte(eventJSON), &event) != nil || event.Data.AgentStatus != "working" || (event.Data.Agent != "" && event.Data.Agent != "codex") {
			return nil
		}
	}
	if paneID == "" || keyPath == "bridge.key" {
		return errors.New("plugin watcher requires HERDR_PANE_ID and HERDR_PLUGIN_CONFIG_DIR")
	}
	return bridge.WatchPane(ctx, client, paneID, keyPath, startedAt, timeout)
}

func takeOption(args []string, name string) (string, []string, error) {
	kept := make([]string, 0, len(args))
	var value string
	for index := 0; index < len(args); index++ {
		if args[index] != name {
			kept = append(kept, args[index])
			continue
		}
		if index+1 >= len(args) {
			return "", nil, fmt.Errorf("missing value for %s", name)
		}
		value = args[index+1]
		index++
	}
	return value, kept, nil
}

func envValue(environ []string, name string) string {
	prefix := name + "="
	for index := len(environ) - 1; index >= 0; index-- {
		if strings.HasPrefix(environ[index], prefix) {
			return strings.TrimPrefix(environ[index], prefix)
		}
	}
	return ""
}

func setEnv(environ []string, name, value string) []string {
	prefix := name + "="
	result := environ[:0]
	for _, item := range environ {
		if !strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}
	return append(result, name+"="+value)
}

func contains(args []string, wanted string) bool {
	for _, arg := range args {
		if arg == wanted {
			return true
		}
	}
	return false
}

func outputFile(writer io.Writer) *os.File {
	if file, ok := writer.(*os.File); ok {
		return file
	}
	return os.Stdout
}
