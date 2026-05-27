package setup

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTestFile(t *testing.T, path string, content string, mode os.FileMode) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create test directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write test file %s: %v", path, err)
	}
}

func TestVersionRequested(t *testing.T) {
	if !versionRequested([]string{"--version"}) {
		t.Fatal("expected --version to request version output")
	}
	if !versionRequested([]string{"-version"}) {
		t.Fatal("expected -version to request version output")
	}
	if versionRequested([]string{"--", "--version"}) {
		t.Fatal("expected -- to stop version flag parsing")
	}
}

func TestPrintVersion(t *testing.T) {
	previousVersion := Version
	previousCommit := Commit
	previousBuildDate := BuildDate
	Version = "v1.2.3"
	Commit = "abc123"
	BuildDate = "2026-05-27T12:00:00Z"
	t.Cleanup(func() {
		Version = previousVersion
		Commit = previousCommit
		BuildDate = previousBuildDate
	})

	var stdout bytes.Buffer
	printVersion(&stdout)

	expected := "setup v1.2.3\ncommit abc123\nbuilt 2026-05-27T12:00:00Z\n"
	if stdout.String() != expected {
		t.Fatalf("version output = %q, want %q", stdout.String(), expected)
	}
}

func minimalSetupYAML() string {
	return strings.Join([]string{
		"version: 1",
		"name: Test Project",
		"variables:",
		"  - name: PodmanService",
		"    flag: podman-service",
		"    prompt: PODMAN_SERVICE",
		"    default: test",
		"  - name: DataDir",
		"    flag: data-dir",
		"    prompt: Data directory",
		"    type: path",
		"    default: \"{{ .ProjectDir }}/data\"",
		"  - name: DomainName",
		"    flag: domain",
		"    prompt: Domain name",
		"    required: true",
		"  - name: UseReverseProxy",
		"    flag: reverse-proxy",
		"    prompt: Use reverse proxy",
		"    type: bool",
		"    default: \"{{ if .FlagValues.ReverseProxyNetworkName }}true{{ else }}false{{ end }}\"",
		"  - name: ReverseProxyNetworkName",
		"    flag: reverse-proxy-network-name",
		"    prompt: External reverse proxy network name",
		"    required_when: \"{{ .UseReverseProxy }}\"",
		"    forbidden_when: \"{{ not .UseReverseProxy }}\"",
		"computed:",
		"  - name: UseLocalSSL",
		"    type: bool",
		"    value: \"{{ not .UseReverseProxy }}\"",
		"passwords:",
		"  - name: AppPassword",
		"    length: 24",
		"directories:",
		"  - path: \"{{ .DataDir }}\"",
		"  - path: \"{{ .DataDir }}/configs\"",
		"assets:",
		"  - source: configs",
		"    target: \"{{ .DataDir }}/configs\"",
		"    exclude:",
		"      names:",
		"        - .gitignore",
		"      suffixes:",
		"        - .tmpl",
		"env:",
		"  source: \".env.tmpl\"",
		"  output: \"{{ .DataDir }}/.env\"",
		"  default_output: \"{{ .DataDir }}/.env_default\"",
		"  backup_name_template: \".env_{{ .Timestamp }}\"",
		"  mode: \"0600\"",
		"  protected:",
		"    suffixes:",
		"      - _IMAGE",
		"    keys:",
		"      - PODMAN_SERVICE",
		"templates:",
		"  - source: compose.yaml.tmpl",
		"    target: \"{{ .DataDir }}/compose.yaml\"",
		"    mode: \"0644\"",
		"state:",
		"  output: \"{{ .DataDir }}/setup.json\"",
		"  mode: \"0644\"",
		"  entries:",
		"    - key: podman_service",
		"      variable: PodmanService",
		"    - key: data_dir",
		"      variable: DataDir",
		"    - key: domain_name",
		"      variable: DomainName",
		"    - key: reverse_proxy_network_name",
		"      variable: ReverseProxyNetworkName",
		"      omit_empty: true",
		"    - key: use_reverse_proxy",
		"      variable: UseReverseProxy",
		"      type: bool",
		"messages:",
		"  - |",
		"    Generated runtime files in {{ .DataDir }}.",
	}, "\n") + "\n"
}

func writeMinimalProject(t *testing.T) (string, string) {
	t.Helper()

	projectDir := t.TempDir()
	configPath := filepath.Join(projectDir, defaultConfigFile)
	writeTestFile(t, configPath, minimalSetupYAML(), 0o644)
	writeTestFile(t, filepath.Join(projectDir, ".env.tmpl"), "PODMAN_SERVICE={{ .PodmanService }}\nDOMAIN_NAME={{ .DomainName }}\nAPP_PASSWORD='{{ .AppPassword }}'\nAPP_IMAGE=docker.io/example/app:latest\nLOCAL_SSL={{ .UseLocalSSL }}\n", 0o600)
	writeTestFile(t, filepath.Join(projectDir, "compose.yaml.tmpl"), "name: {{ .PodmanService }}\n", 0o644)
	writeTestFile(t, filepath.Join(projectDir, "configs", "app.conf"), "config\n", 0o644)
	writeTestFile(t, filepath.Join(projectDir, "configs", "skip.tmpl"), "skip\n", 0o644)
	writeTestFile(t, filepath.Join(projectDir, "configs", ".gitignore"), "*\n", 0o644)

	return projectDir, configPath
}

func assertQuotedPassword(t *testing.T, value string) {
	t.Helper()

	if !strings.HasPrefix(value, "'") || !strings.HasSuffix(value, "'") {
		t.Fatalf("password %q is not single-quoted", value)
	}
	if len(value) != defaultPasswordChars+2 {
		t.Fatalf("password %q has length %d, want %d", value, len(value), defaultPasswordChars+2)
	}
	if strings.Contains(value[1:len(value)-1], "'") {
		t.Fatalf("password %q contains an embedded single quote", value)
	}
	for _, character := range value[1 : len(value)-1] {
		if !strings.ContainsRune(passwordAllowedChars, character) {
			t.Fatalf("password %q contains disallowed character %q", value, character)
		}
	}
}

func TestParseOptionsQuietSuppressesHelp(t *testing.T) {
	_, configPath := writeMinimalProject(t)

	var stdout bytes.Buffer
	_, _, err := parseOptions([]string{"--config", configPath, "--quiet", "--help"}, &stdout)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("parse options error = %v, want %v", err, flag.ErrHelp)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected quiet help to produce no output, got %q", stdout.String())
	}
}

func TestRunUsesSetupYAMLAndSuppressesQuietOutput(t *testing.T) {
	projectDir, configPath := writeMinimalProject(t)
	dataDir := filepath.Join(t.TempDir(), "data")

	var stdout bytes.Buffer
	err := run([]string{
		"--config", configPath,
		"--quiet",
		"--non-interactive",
		"--project-dir", projectDir,
		"--data-dir", dataDir,
		"--domain", "example.test",
	}, strings.NewReader(""), &stdout)
	if err != nil {
		t.Fatalf("run quiet setup: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected quiet setup to produce no output, got %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(dataDir, "setup.json")); err != nil {
		t.Fatalf("expected setup json to be written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "configs", "app.conf")); err != nil {
		t.Fatalf("expected config asset to be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "configs", "skip.tmpl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected template asset to be skipped, stat error = %v", err)
	}
}

func TestParseOptionsImportsValuesFromSetupJSON(t *testing.T) {
	_, configPath := writeMinimalProject(t)
	setupJSONPath := filepath.Join(t.TempDir(), "setup.json")
	writeTestFile(t, setupJSONPath, "{\n  \"podman_service\": \"website\",\n  \"domain_name\": \"imported.test\",\n  \"use_reverse_proxy\": true,\n  \"reverse_proxy_network_name\": \"proxy\"\n}\n", 0o644)

	var stdout bytes.Buffer
	config, opts, err := parseOptions([]string{"--config", configPath, "--json", setupJSONPath, "--non-interactive"}, &stdout)
	if err != nil {
		t.Fatalf("parse options from setup json: %v", err)
	}
	data, err := collectSetupData(config, opts, prompt{})
	if err != nil {
		t.Fatalf("collect imported setup data: %v", err)
	}
	if data["PodmanService"] != "website" {
		t.Fatalf("podman service = %q, want %q", data["PodmanService"], "website")
	}
	if data["DomainName"] != "imported.test" {
		t.Fatalf("domain name = %q, want %q", data["DomainName"], "imported.test")
	}
	if data["UseReverseProxy"] != true {
		t.Fatalf("use reverse proxy = %v, want true", data["UseReverseProxy"])
	}
	if data["ReverseProxyNetworkName"] != "proxy" {
		t.Fatalf("reverse proxy network name = %q, want %q", data["ReverseProxyNetworkName"], "proxy")
	}
}

func TestRunDoesNotPromptForForbiddenReverseProxyNetworkName(t *testing.T) {
	projectDir, configPath := writeMinimalProject(t)
	setupJSONPath := filepath.Join(t.TempDir(), "setup.json")
	writeTestFile(t, setupJSONPath, "{\n  \"domain_name\": \"imported.test\",\n  \"use_reverse_proxy\": false\n}\n", 0o644)

	var stdout bytes.Buffer
	err := run([]string{
		"--config", configPath,
		"--project-dir", projectDir,
		"--json", setupJSONPath,
	}, strings.NewReader("\n\n\n"), &stdout)
	if err != nil {
		t.Fatalf("run setup from json with disabled reverse proxy: %v", err)
	}
	if strings.Contains(stdout.String(), "External reverse proxy network name") {
		t.Fatalf("unexpected reverse proxy network prompt in output: %q", stdout.String())
	}
}

func TestValueFromPromptOrDefaultShowsResolvedPathDefault(t *testing.T) {
	projectDir := filepath.Join(t.TempDir(), "project")
	expectedDefault, err := filepath.Abs(filepath.Join(projectDir, "..", "resolved-data"))
	if err != nil {
		t.Fatalf("resolve expected default path: %v", err)
	}

	variable := variableConfig{
		Name:    "DataDir",
		Prompt:  "Data directory",
		Type:    "path",
		Default: "{{ .ProjectDir }}/../resolved-data",
	}

	var stdout bytes.Buffer
	value, err := valueFromPromptOrDefault(variable, map[string]any{"ProjectDir": projectDir}, false, prompt{
		reader: bufio.NewReader(strings.NewReader("\n")),
		output: &stdout,
	})
	if err != nil {
		t.Fatalf("valueFromPromptOrDefault: %v", err)
	}
	if value != expectedDefault {
		t.Fatalf("default value = %q, want %q", value, expectedDefault)
	}
	if !strings.Contains(stdout.String(), "Data directory ["+expectedDefault+"]: ") {
		t.Fatalf("prompt output = %q, want resolved default path", stdout.String())
	}
	if strings.Contains(stdout.String(), "/../") {
		t.Fatalf("prompt output still contains unresolved parent segment: %q", stdout.String())
	}
}

func TestImportedReverseProxyNetworkNameDoesNotChangeBoolDefaultWithoutFlag(t *testing.T) {
	_, configPath := writeMinimalProject(t)
	setupJSONPath := filepath.Join(t.TempDir(), "setup.json")
	writeTestFile(t, setupJSONPath, "{\n  \"domain_name\": \"imported.test\",\n  \"reverse_proxy_network_name\": \"proxy\"\n}\n", 0o644)

	var stdout bytes.Buffer
	config, opts, err := parseOptions([]string{"--config", configPath, "--json", setupJSONPath}, &stdout)
	if err != nil {
		t.Fatalf("parse options from setup json: %v", err)
	}
	data, err := collectSetupData(config, opts, prompt{
		reader: bufio.NewReader(strings.NewReader("\n\n\n")),
		output: &stdout,
	})
	if err != nil {
		t.Fatalf("collect setup data: %v", err)
	}
	if data["UseReverseProxy"] != false {
		t.Fatalf("use reverse proxy = %v, want false", data["UseReverseProxy"])
	}
	if data["ReverseProxyNetworkName"] != "" {
		t.Fatalf("reverse proxy network name = %q, want empty", data["ReverseProxyNetworkName"])
	}
	if !strings.Contains(stdout.String(), "Use reverse proxy (true/false) [false]: ") {
		t.Fatalf("prompt output = %q, want false default", stdout.String())
	}
	if strings.Contains(stdout.String(), "External reverse proxy network name") {
		t.Fatalf("unexpected reverse proxy network prompt in output: %q", stdout.String())
	}
}

func TestValueFromPromptOrDefaultUsesTrueFalsePromptForBool(t *testing.T) {
	variable := variableConfig{
		Name:    "UseReverseProxy",
		Prompt:  "Use an external reverse proxy network",
		Type:    "bool",
		Default: "false",
	}

	var stdout bytes.Buffer
	value, err := valueFromPromptOrDefault(variable, map[string]any{}, false, prompt{
		reader: bufio.NewReader(strings.NewReader("\n")),
		output: &stdout,
	})
	if err != nil {
		t.Fatalf("valueFromPromptOrDefault bool: %v", err)
	}
	if value != "false" {
		t.Fatalf("bool default value = %q, want %q", value, "false")
	}
	if !strings.Contains(stdout.String(), "Use an external reverse proxy network (true/false) [false]: ") {
		t.Fatalf("prompt output = %q, want true/false suffix", stdout.String())
	}
}

func TestParseBoolRejectsYesNoValues(t *testing.T) {
	if _, err := parseBool("yes"); err == nil {
		t.Fatal("expected yes to be rejected")
	}
	if _, err := parseBool("no"); err == nil {
		t.Fatal("expected no to be rejected")
	}
}

func TestNormalizeChoiceUsesPlainValuesOnly(t *testing.T) {
	variable := variableConfig{
		Name:    "DatabaseSQL",
		Choices: []string{"mariadb", "postgresql"},
	}

	value, err := normalizeChoice("mariadb", variable)
	if err != nil {
		t.Fatalf("normalizeChoice valid value: %v", err)
	}
	if value != "mariadb" {
		t.Fatalf("normalizeChoice returned %q, want %q", value, "mariadb")
	}

	if _, err := normalizeChoice("mysql", variable); err == nil {
		t.Fatal("expected removed alias mysql to be rejected")
	}
}

func TestRunGeneratesPasswordsEnvTemplatesAndState(t *testing.T) {
	projectDir, configPath := writeMinimalProject(t)
	dataDir := filepath.Join(t.TempDir(), "data")

	err := run([]string{
		"--config", configPath,
		"--non-interactive",
		"--project-dir", projectDir,
		"--data-dir", dataDir,
		"--podman-service", "website",
		"--domain", "example.test",
	}, strings.NewReader(""), io.Discard)
	if err != nil {
		t.Fatalf("run setup: %v", err)
	}

	envContent, err := os.ReadFile(filepath.Join(dataDir, ".env"))
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	assignments := parseEnvAssignments(envContent)
	if assignments["PODMAN_SERVICE"] != "website" {
		t.Fatalf("PODMAN_SERVICE = %q, want website", assignments["PODMAN_SERVICE"])
	}
	if assignments["DOMAIN_NAME"] != "example.test" {
		t.Fatalf("DOMAIN_NAME = %q, want example.test", assignments["DOMAIN_NAME"])
	}
	assertQuotedPassword(t, assignments["APP_PASSWORD"])
	if assignments["LOCAL_SSL"] != "true" {
		t.Fatalf("LOCAL_SSL = %q, want true", assignments["LOCAL_SSL"])
	}

	composeContent, err := os.ReadFile(filepath.Join(dataDir, "compose.yaml"))
	if err != nil {
		t.Fatalf("read compose file: %v", err)
	}
	if string(composeContent) != "name: website\n" {
		t.Fatalf("unexpected compose content:\n%s", string(composeContent))
	}

	setupJSONContent, err := os.ReadFile(filepath.Join(dataDir, "setup.json"))
	if err != nil {
		t.Fatalf("read setup json: %v", err)
	}
	var state map[string]any
	if err := json.Unmarshal(setupJSONContent, &state); err != nil {
		t.Fatalf("parse setup json: %v", err)
	}
	if state["podman_service"] != "website" || state["domain_name"] != "example.test" {
		t.Fatalf("unexpected setup json values: %+v", state)
	}
}

func TestWriteMergedEnvFilePreservesExistingValuesExceptProtected(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, defaultGeneratedEnvFile)

	existingContent := "PODMAN_SERVICE=existing-service\n" +
		"APP_PASSWORD='existing-password'\n" +
		"APP_IMAGE=docker.io/example/app:old\n" +
		"APP_PATH=/existing\n"
	if err := os.WriteFile(envPath, []byte(existingContent), 0o600); err != nil {
		t.Fatalf("write existing env: %v", err)
	}

	renderedContent := []byte("PODMAN_SERVICE=generated-service\n" +
		"APP_PASSWORD='generated-password'\n" +
		"APP_IMAGE=docker.io/example/app:new\n" +
		"APP_PATH=/generated\n")

	previousNowFunc := nowFunc
	nowFunc = func() time.Time {
		return time.Date(2026, time.May, 8, 14, 30, 45, 0, time.UTC)
	}
	t.Cleanup(func() {
		nowFunc = previousNowFunc
	})

	env := envConfig{
		DefaultOutput:      filepath.Join(tempDir, defaultEnvDefaultFile),
		BackupNameTemplate: ".env_{{ .Timestamp }}",
		Protected: protectedConfig{
			Suffixes: []string{"_IMAGE"},
			Keys:     []string{"PODMAN_SERVICE"},
		},
	}
	if err := writeMergedEnvFile(envPath, renderedContent, 0o600, env, map[string]any{"ProjectDir": tempDir}); err != nil {
		t.Fatalf("write merged env: %v", err)
	}

	defaultContent, err := os.ReadFile(filepath.Join(tempDir, defaultEnvDefaultFile))
	if err != nil {
		t.Fatalf("read env default: %v", err)
	}
	if string(defaultContent) != string(renderedContent) {
		t.Fatalf("unexpected env default content:\n%s", string(defaultContent))
	}

	backupContent, err := os.ReadFile(filepath.Join(tempDir, ".env_2026-05-08_14-30-45"))
	if err != nil {
		t.Fatalf("read env backup: %v", err)
	}
	if string(backupContent) != existingContent {
		t.Fatalf("unexpected env backup content:\n%s", string(backupContent))
	}

	mergedContent, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read merged env: %v", err)
	}
	expectedMergedContent := "PODMAN_SERVICE=generated-service\n" +
		"APP_PASSWORD='existing-password'\n" +
		"APP_IMAGE=docker.io/example/app:new\n" +
		"APP_PATH=/existing\n"
	if string(mergedContent) != expectedMergedContent {
		t.Fatalf("unexpected merged env content:\n%s", string(mergedContent))
	}
}

func TestWriteMergedEnvFileSkipsDefaultWhenRenderedMatchesFinalEnv(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, defaultGeneratedEnvFile)
	defaultPath := filepath.Join(tempDir, defaultEnvDefaultFile)

	existingContent := "APP_PASSWORD='existing-password'\n"
	if err := os.WriteFile(envPath, []byte(existingContent), 0o600); err != nil {
		t.Fatalf("write existing env: %v", err)
	}
	if err := os.WriteFile(defaultPath, []byte("stale\n"), 0o600); err != nil {
		t.Fatalf("write stale env default: %v", err)
	}

	renderedContent := []byte("APP_PASSWORD='generated-password'\n")

	previousNowFunc := nowFunc
	nowFunc = func() time.Time {
		return time.Date(2026, time.May, 8, 15, 30, 45, 0, time.UTC)
	}
	t.Cleanup(func() {
		nowFunc = previousNowFunc
	})

	env := envConfig{
		DefaultOutput:      defaultPath,
		BackupNameTemplate: ".env_{{ .Timestamp }}",
		Protected: protectedConfig{
			Suffixes: []string{"_PASSWORD"},
		},
	}
	if err := writeMergedEnvFile(envPath, renderedContent, 0o600, env, map[string]any{"ProjectDir": tempDir, "SetupJSONProvided": true}); err != nil {
		t.Fatalf("write merged env: %v", err)
	}

	if _, err := os.Stat(defaultPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected env default to be absent, stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(tempDir, ".env_2026-05-08_15-30-45")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no env backup when content is unchanged, stat error = %v", err)
	}

	mergedContent, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read merged env: %v", err)
	}
	if string(mergedContent) != existingContent {
		t.Fatalf("unexpected merged env content:\n%s", string(mergedContent))
	}
}

func TestWriteMergedEnvFileReusesExistingPasswordsForSetupJSONReplay(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, defaultGeneratedEnvFile)

	existingContent := "APP_PASSWORD='existing-password'\n"
	if err := os.WriteFile(envPath, []byte(existingContent), 0o600); err != nil {
		t.Fatalf("write existing env: %v", err)
	}

	renderedContent := []byte("APP_PASSWORD='generated-password'\n")

	env := envConfig{
		Protected: protectedConfig{
			Suffixes: []string{"_PASSWORD"},
		},
	}
	if err := writeMergedEnvFile(envPath, renderedContent, 0o600, env, map[string]any{"ProjectDir": tempDir, "SetupJSONProvided": true}); err != nil {
		t.Fatalf("write merged env: %v", err)
	}

	mergedContent, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read merged env: %v", err)
	}
	if string(mergedContent) != existingContent {
		t.Fatalf("unexpected merged env content:\n%s", string(mergedContent))
	}
}

func TestWriteMergedEnvFileReplayUsesConfiguredPasswordSelectors(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, defaultGeneratedEnvFile)

	existingContent := "APP_PASSWORD='existing-password'\n"
	if err := os.WriteFile(envPath, []byte(existingContent), 0o600); err != nil {
		t.Fatalf("write existing env: %v", err)
	}

	renderedContent := []byte("APP_PASSWORD='generated-password'\n")

	env := envConfig{
		Protected: protectedConfig{
			Suffixes: []string{"_PASSWORD"},
		},
	}
	if err := writeMergedEnvFile(envPath, renderedContent, 0o600, env, map[string]any{"ProjectDir": tempDir, "SetupJSONProvided": true}); err != nil {
		t.Fatalf("write merged env: %v", err)
	}

	mergedContent, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read merged env: %v", err)
	}
	if string(mergedContent) != existingContent {
		t.Fatalf("unexpected merged env content:\n%s", string(mergedContent))
	}
}

func TestProtectedEnvKeyUsesConfig(t *testing.T) {
	protected := protectedConfig{
		Prefixes: []string{"CUSTOM_PREFIX_"},
		Suffixes: []string{"_IMAGE"},
		Keys:     []string{"PODMAN_SERVICE"},
	}

	testCases := []struct {
		key       string
		protected bool
	}{
		{key: "CUSTOM_PREFIX_VALUE", protected: true},
		{key: "PODMAN_SERVICE", protected: true},
		{key: "APP_IMAGE", protected: true},
		{key: "APP_PATH", protected: false},
	}

	for _, testCase := range testCases {
		if got := isProtectedEnvKey(testCase.key, protected); got != testCase.protected {
			t.Fatalf("isProtectedEnvKey(%q) = %t, want %t", testCase.key, got, testCase.protected)
		}
	}
}
