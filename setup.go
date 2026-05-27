package setup

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/goccy/go-yaml"
)

const (
	defaultConfigFile       = "setup.yaml"
	defaultGeneratedEnvFile = ".env"
	defaultEnvDefaultFile   = ".env_default"
	defaultFileMode         = 0o644
	defaultEnvFileMode      = 0o600
	defaultPasswordChars    = 24
	passwordAlpha           = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	passwordNumbers         = "0123456789"
	passwordSpecialChars    = "!@#$%^&*()_+-=[]{}|;:,.<>/?~"
	passwordAllowedChars    = passwordAlpha + passwordNumbers + passwordSpecialChars
)

var nowFunc = time.Now

type options struct {
	ConfigPath          string
	Force               bool
	NonInteractive      bool
	ProjectDir          string
	Quiet               bool
	SetupJSON           string
	VariableValues      map[string]string
	OverriddenVariables map[string]bool
	VisitedFlags        map[string]bool
}

type bootstrapOptions struct {
	ConfigPath string
	Quiet      bool
}

type setupConfig struct {
	Version     int              `yaml:"version"`
	Name        string           `yaml:"name"`
	Variables   []variableConfig `yaml:"variables"`
	Computed    []computedConfig `yaml:"computed"`
	Passwords   []passwordConfig `yaml:"passwords"`
	Directories []pathConfig     `yaml:"directories"`
	Assets      []assetConfig    `yaml:"assets"`
	Env         envConfig        `yaml:"env"`
	Templates   []templateConfig `yaml:"templates"`
	State       stateConfig      `yaml:"state"`
	Messages    []string         `yaml:"messages"`
}

type variableConfig struct {
	Name             string   `yaml:"name"`
	Flag             string   `yaml:"flag"`
	Prompt           string   `yaml:"prompt"`
	Description      string   `yaml:"description"`
	Type             string   `yaml:"type"`
	Default          string   `yaml:"default"`
	Required         bool     `yaml:"required"`
	RequiredWhen     string   `yaml:"required_when"`
	When             string   `yaml:"when"`
	ForbiddenWhen    string   `yaml:"forbidden_when"`
	ForbiddenMessage string   `yaml:"forbidden_message"`
	Choices          []string `yaml:"choices"`
	InvalidMessage   string   `yaml:"invalid_message"`
}

type computedConfig struct {
	Name  string `yaml:"name"`
	Type  string `yaml:"type"`
	Value string `yaml:"value"`
}

type passwordConfig struct {
	Name         string `yaml:"name"`
	When         string `yaml:"when"`
	Length       int    `yaml:"length"`
	AllowedChars string `yaml:"allowed_chars"`
}

type pathConfig struct {
	Path string `yaml:"path"`
	When string `yaml:"when"`
	Mode string `yaml:"mode"`
}

type assetConfig struct {
	Source  string        `yaml:"source"`
	Target  string        `yaml:"target"`
	When    string        `yaml:"when"`
	Exclude excludeConfig `yaml:"exclude"`
}

type excludeConfig struct {
	Names    []string `yaml:"names"`
	Suffixes []string `yaml:"suffixes"`
}

type envConfig struct {
	Source             string          `yaml:"source"`
	Output             string          `yaml:"output"`
	DefaultOutput      string          `yaml:"default_output"`
	BackupNameTemplate string          `yaml:"backup_name_template"`
	Mode               string          `yaml:"mode"`
	Protected          protectedConfig `yaml:"protected"`
}

type protectedConfig struct {
	Prefixes []string `yaml:"prefixes"`
	Suffixes []string `yaml:"suffixes"`
	Keys     []string `yaml:"keys"`
}

type templateConfig struct {
	Source string `yaml:"source"`
	Target string `yaml:"target"`
	When   string `yaml:"when"`
	Mode   string `yaml:"mode"`
}

type stateConfig struct {
	Output  string             `yaml:"output"`
	Mode    string             `yaml:"mode"`
	Entries []stateEntryConfig `yaml:"entries"`
}

type stateEntryConfig struct {
	Key       string `yaml:"key"`
	Variable  string `yaml:"variable"`
	Value     string `yaml:"value"`
	Type      string `yaml:"type"`
	OmitEmpty bool   `yaml:"omit_empty"`
}

type prompt struct {
	reader *bufio.Reader
	output io.Writer
}

type copySummary struct {
	Copied  int
	Skipped int
}

func Main() {
	quiet := quietRequested(os.Args[1:])
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}

		if !quiet {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	config, opts, err := parseOptions(args, stdout)
	if err != nil {
		return err
	}
	if opts.Quiet {
		stdout = io.Discard
	}

	prompter := prompt{
		reader: bufio.NewReader(stdin),
		output: stdout,
	}

	data, err := collectSetupData(config, opts, prompter)
	if err != nil {
		return err
	}

	if err := prepareDirectories(config.Directories, data); err != nil {
		return err
	}

	for _, asset := range config.Assets {
		summary, err := copyConfigAsset(asset, data, opts.Force)
		if err != nil {
			return err
		}
		if summary.Copied > 0 || summary.Skipped > 0 {
			fmt.Fprintf(stdout, "Seeded config assets: %d copied, %d kept.\n", summary.Copied, summary.Skipped)
		}
	}

	if err := renderEnvFile(config.Env, data); err != nil {
		return err
	}
	if err := renderTemplateFiles(config.Templates, data); err != nil {
		return err
	}
	if err := writeStateFile(config.State, data); err != nil {
		return err
	}

	return printMessages(config.Messages, data, stdout)
}

func parseOptions(args []string, stdout io.Writer) (setupConfig, options, error) {
	bootstrap := parseBootstrapOptions(args)
	configPath, err := resolveConfigPath(bootstrap.ConfigPath)
	if err != nil {
		return setupConfig{}, options{}, err
	}

	config, err := loadSetupConfig(configPath)
	if err != nil {
		return setupConfig{}, options{}, err
	}

	defaultProjectDir := filepath.Dir(configPath)
	opts := options{
		ConfigPath:          configPath,
		ProjectDir:          defaultProjectDir,
		Quiet:               bootstrap.Quiet,
		VariableValues:      map[string]string{},
		OverriddenVariables: map[string]bool{},
		VisitedFlags:        map[string]bool{},
	}

	flagSet := flag.NewFlagSet("container-setup", flag.ContinueOnError)
	if opts.Quiet {
		flagSet.SetOutput(io.Discard)
	} else {
		flagSet.SetOutput(stdout)
	}

	flagSet.StringVar(&opts.ConfigPath, "config", configPath, "setup YAML file")
	flagSet.StringVar(&opts.ProjectDir, "project-dir", defaultProjectDir, "repository directory used for relative templates and assets")
	flagSet.StringVar(&opts.SetupJSON, "json", "", "import setup values from a generated setup.json file")
	flagSet.BoolVar(&opts.Force, "force", false, "overwrite copied config assets that already exist")
	flagSet.BoolVar(&opts.NonInteractive, "non-interactive", false, "do not prompt; require values without defaults as flags or imported state")
	flagSet.BoolVar(&opts.Quiet, "quiet", opts.Quiet, "suppress all command output")

	variableFlagValues := map[string]*string{}
	variableFlags := map[string]string{}
	for _, variable := range config.Variables {
		if variable.Flag == "" {
			continue
		}
		if _, exists := variableFlags[variable.Flag]; exists {
			return setupConfig{}, options{}, fmt.Errorf("duplicate setup flag %q", variable.Flag)
		}
		value := ""
		variableFlagValues[variable.Name] = &value
		variableFlags[variable.Flag] = variable.Name
		description := variable.Description
		if description == "" {
			description = variable.Prompt
		}
		if description == "" {
			description = variable.Name
		}
		flagSet.StringVar(&value, variable.Flag, "", description)
	}

	flagSet.Usage = func() {
		name := config.Name
		if name == "" {
			name = "container project"
		}

		fmt.Fprintf(flagSet.Output(), "Usage:\n")
		fmt.Fprintf(flagSet.Output(), "  %s [flags]\n\n", filepath.Base(os.Args[0]))
		fmt.Fprintf(flagSet.Output(), "Config:\n")
		fmt.Fprintf(flagSet.Output(), "  %s\n\n", configPath)
		fmt.Fprintf(flagSet.Output(), "Project:\n")
		fmt.Fprintf(flagSet.Output(), "  %s\n\n", name)
		fmt.Fprintf(flagSet.Output(), "Flags:\n")
		flagSet.PrintDefaults()
	}

	if err := flagSet.Parse(args); err != nil {
		return setupConfig{}, options{}, err
	}

	opts.VisitedFlags = visitedFlags(flagSet)
	for flagName, variableName := range variableFlags {
		if !opts.VisitedFlags[flagName] {
			continue
		}
		opts.VariableValues[variableName] = *variableFlagValues[variableName]
		opts.OverriddenVariables[variableName] = true
	}

	if opts.SetupJSON != "" {
		if err := mergeImportedState(&opts, config.State, config.Variables); err != nil {
			return setupConfig{}, options{}, err
		}
	}

	return config, opts, nil
}

func parseBootstrapOptions(args []string) bootstrapOptions {
	return bootstrapOptions{
		ConfigPath: configPathRequested(args),
		Quiet:      quietRequested(args),
	}
}

func configPathRequested(args []string) string {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			break
		}

		switch {
		case arg == "-config" || arg == "--config":
			if index+1 < len(args) {
				return args[index+1]
			}
		case strings.HasPrefix(arg, "-config="):
			return strings.TrimPrefix(arg, "-config=")
		case strings.HasPrefix(arg, "--config="):
			return strings.TrimPrefix(arg, "--config=")
		}
	}

	return ""
}

func quietRequested(args []string) bool {
	quiet := false
	for _, arg := range args {
		if arg == "--" {
			break
		}

		switch {
		case arg == "-quiet" || arg == "--quiet":
			quiet = true
		case strings.HasPrefix(arg, "-quiet="):
			quiet = parseQuietFlagValue(strings.TrimPrefix(arg, "-quiet="))
		case strings.HasPrefix(arg, "--quiet="):
			quiet = parseQuietFlagValue(strings.TrimPrefix(arg, "--quiet="))
		}
	}

	return quiet
}

func parseQuietFlagValue(value string) bool {
	quiet, err := strconv.ParseBool(value)
	if err != nil {
		return true
	}

	return quiet
}

func visitedFlags(flagSet *flag.FlagSet) map[string]bool {
	flags := make(map[string]bool)
	flagSet.Visit(func(flag *flag.Flag) {
		flags[flag.Name] = true
	})

	return flags
}

func resolveConfigPath(path string) (string, error) {
	if strings.TrimSpace(path) != "" {
		return absolutePath(path, "")
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get current directory: %w", err)
	}

	if found, ok := findFileUpward(workingDir, defaultConfigFile); ok {
		return found, nil
	}

	executablePath, err := os.Executable()
	if err == nil {
		if found, ok := findFileUpward(filepath.Dir(executablePath), defaultConfigFile); ok {
			return found, nil
		}
	}

	return filepath.Join(workingDir, defaultConfigFile), nil
}

func findFileUpward(startDir string, name string) (string, bool) {
	currentDir, err := filepath.Abs(startDir)
	if err != nil {
		return "", false
	}

	for {
		candidate := filepath.Join(currentDir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			return "", false
		}
		currentDir = parentDir
	}
}

func loadSetupConfig(path string) (setupConfig, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return setupConfig{}, fmt.Errorf("read setup config %s: %w", path, err)
	}

	var config setupConfig
	if err := yaml.Unmarshal(content, &config); err != nil {
		return setupConfig{}, fmt.Errorf("parse setup config %s: %w", path, err)
	}

	if config.Version == 0 {
		return setupConfig{}, errors.New("setup config version is required")
	}

	return config, nil
}

func mergeImportedState(opts *options, state stateConfig, variables []variableConfig) error {
	statePath, err := absolutePath(opts.SetupJSON, "")
	if err != nil {
		return fmt.Errorf("resolve setup json path: %w", err)
	}

	content, err := os.ReadFile(statePath)
	if err != nil {
		return fmt.Errorf("read setup json %s: %w", statePath, err)
	}

	var importedData map[string]any
	if err := json.Unmarshal(content, &importedData); err != nil {
		return fmt.Errorf("parse setup json %s: %w", statePath, err)
	}

	variableNames := map[string]bool{}
	for _, variable := range variables {
		variableNames[variable.Name] = true
	}

	for _, entry := range state.Entries {
		if entry.Key == "" || entry.Variable == "" {
			continue
		}

		value, found := importedData[entry.Key]
		if !found {
			continue
		}

		stringValue := stringifyStateValue(value)
		switch entry.Variable {
		case "ProjectDir":
			if !opts.VisitedFlags["project-dir"] && strings.TrimSpace(stringValue) != "" {
				opts.ProjectDir = stringValue
			}
		default:
			if !variableNames[entry.Variable] || opts.OverriddenVariables[entry.Variable] {
				continue
			}
			if strings.TrimSpace(stringValue) == "" && entry.Type != "bool" {
				continue
			}
			opts.VariableValues[entry.Variable] = stringValue
		}
	}

	return nil
}

func stringifyStateValue(value any) string {
	switch typedValue := value.(type) {
	case string:
		return typedValue
	case bool:
		return strconv.FormatBool(typedValue)
	case float64:
		if typedValue == float64(int64(typedValue)) {
			return strconv.FormatInt(int64(typedValue), 10)
		}
		return strconv.FormatFloat(typedValue, 'f', -1, 64)
	default:
		return fmt.Sprint(typedValue)
	}
}

func collectSetupData(config setupConfig, opts options, prompter prompt) (map[string]any, error) {
	projectDir, err := absolutePath(opts.ProjectDir, "")
	if err != nil {
		return nil, fmt.Errorf("resolve project directory: %w", err)
	}
	configPath, err := absolutePath(opts.ConfigPath, projectDir)
	if err != nil {
		return nil, fmt.Errorf("resolve setup config path: %w", err)
	}

	data := map[string]any{
		"ConfigPath":        configPath,
		"ConfigDir":         filepath.Dir(configPath),
		"ProjectDir":        projectDir,
		"CurrentUserUID":    strconv.Itoa(os.Getuid()),
		"CurrentGroupGID":   strconv.Itoa(os.Getgid()),
		"SetupJSONProvided": strings.TrimSpace(opts.SetupJSON) != "",
	}
	flagValues := map[string]string{}
	for _, variable := range config.Variables {
		flagValues[variable.Name] = ""
	}
	for name, overridden := range opts.OverriddenVariables {
		if !overridden {
			continue
		}
		flagValues[name] = opts.VariableValues[name]
	}
	data["FlagValues"] = flagValues
	for _, variable := range config.Variables {
		data[variable.Name] = ""
	}
	for name, value := range opts.VariableValues {
		data[name] = value
	}

	for _, variable := range config.Variables {
		shouldCollect, err := conditionMatches(variable.When, data, true)
		if err != nil {
			return nil, fmt.Errorf("evaluate condition for %s: %w", variable.Name, err)
		}
		if !shouldCollect {
			data[variable.Name] = ""
			continue
		}

		value, supplied := opts.VariableValues[variable.Name]
		if supplied && !opts.OverriddenVariables[variable.Name] {
			forbidden, err := conditionMatches(variable.ForbiddenWhen, data, false)
			if err != nil {
				return nil, fmt.Errorf("evaluate forbidden condition for %s: %w", variable.Name, err)
			}
			if forbidden {
				data[variable.Name] = ""
				continue
			}
		}
		if !supplied {
			value, err = valueFromPromptOrDefault(variable, data, opts.NonInteractive, prompter)
			if err != nil {
				return nil, err
			}
		}

		normalized, err := normalizeVariableValue(variable, value, data)
		if err != nil && !opts.NonInteractive && variable.Prompt != "" {
			for err != nil {
				message := variable.InvalidMessage
				if message == "" {
					message = err.Error()
				}
				fmt.Fprintln(prompter.output, message)
				value, err = prompter.required(variable.Prompt)
				if err != nil {
					return nil, err
				}
				normalized, err = normalizeVariableValue(variable, value, data)
			}
		} else if err != nil {
			return nil, err
		}

		data[variable.Name] = normalized

		if err := validateVariablePresence(variable, normalized, data); err != nil {
			return nil, err
		}
	}

	for _, computed := range config.Computed {
		value, err := renderStringTemplate("computed "+computed.Name, computed.Value, data)
		if err != nil {
			return nil, err
		}
		normalized, err := normalizeTypedValue(computed.Type, value, computed.Name, data)
		if err != nil {
			return nil, err
		}
		data[computed.Name] = normalized
	}

	passwords := map[string]string{}
	for _, password := range config.Passwords {
		shouldGenerate, err := conditionMatches(password.When, data, true)
		if err != nil {
			return nil, fmt.Errorf("evaluate condition for password %s: %w", password.Name, err)
		}
		if !shouldGenerate {
			continue
		}

		length := password.Length
		if length == 0 {
			length = defaultPasswordChars
		}
		allowedChars := password.AllowedChars
		if allowedChars == "" {
			allowedChars = passwordAllowedChars
		}

		value, err := generateSecurePassword(length, allowedChars)
		if err != nil {
			return nil, fmt.Errorf("generate password for %s: %w", password.Name, err)
		}
		passwords[password.Name] = value
		data[password.Name] = value
	}
	data["Password"] = passwords

	return data, nil
}

func valueFromPromptOrDefault(variable variableConfig, data map[string]any, nonInteractive bool, prompter prompt) (string, error) {
	defaultValue := ""
	if variable.Default != "" {
		value, err := renderStringTemplate("default "+variable.Name, variable.Default, data)
		if err != nil {
			return "", err
		}
		normalizedDefault, err := normalizeVariableValue(variable, value, data)
		if err != nil {
			return "", err
		}
		defaultValue = fmt.Sprint(normalizedDefault)
	}

	required, err := variableRequired(variable, data)
	if err != nil {
		return "", err
	}

	forbidden, err := conditionMatches(variable.ForbiddenWhen, data, false)
	if err != nil {
		return "", err
	}
	if forbidden {
		return "", nil
	}

	if nonInteractive {
		if defaultValue != "" || variable.Default != "" {
			return defaultValue, nil
		}
		if required {
			return "", fmt.Errorf("missing --%s", variable.Flag)
		}
		return "", nil
	}

	if variable.Prompt == "" {
		return defaultValue, nil
	}

	if strings.EqualFold(variable.Type, "bool") {
		return prompter.boolean(variable.Prompt, defaultValue, required)
	}

	if required && defaultValue == "" {
		return prompter.required(variable.Prompt)
	}

	return prompter.optional(variable.Prompt, defaultValue)
}

func variableRequired(variable variableConfig, data map[string]any) (bool, error) {
	if variable.Required {
		return true, nil
	}

	return conditionMatches(variable.RequiredWhen, data, false)
}

func normalizeVariableValue(variable variableConfig, value string, data map[string]any) (any, error) {
	value = strings.TrimSpace(value)
	if len(variable.Choices) > 0 {
		normalized, err := normalizeChoice(value, variable)
		if err != nil {
			return nil, err
		}
		value = normalized
	}

	return normalizeTypedValue(variable.Type, value, variable.Name, data)
}

func normalizeChoice(value string, variable variableConfig) (string, error) {
	for _, choice := range variable.Choices {
		if strings.EqualFold(value, choice) {
			return choice, nil
		}
	}

	choices := make([]string, 0, len(variable.Choices))
	for _, choice := range variable.Choices {
		choices = append(choices, choice)
	}
	return "", fmt.Errorf("unsupported %s %q; use %s", variable.Name, value, strings.Join(choices, " or "))
}

func normalizeTypedValue(valueType string, value string, name string, data map[string]any) (any, error) {
	switch strings.ToLower(valueType) {
	case "", "string":
		return value, nil
	case "path":
		if value == "" {
			return "", nil
		}
		projectDir, _ := data["ProjectDir"].(string)
		resolved, err := absolutePath(value, projectDir)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", name, err)
		}
		return resolved, nil
	case "bool":
		if value == "" {
			return false, nil
		}
		parsed, err := parseBool(value)
		if err != nil {
			return nil, err
		}
		return parsed, nil
	case "int", "number":
		if value == "" {
			return "", nil
		}
		id, err := strconv.Atoi(value)
		if err != nil || id < 0 {
			return nil, fmt.Errorf("%s must be a non-negative number", name)
		}
		return value, nil
	default:
		return nil, fmt.Errorf("unsupported type %q for %s", valueType, name)
	}
}

func validateVariablePresence(variable variableConfig, value any, data map[string]any) error {
	stringValue := strings.TrimSpace(fmt.Sprint(value))
	required, err := variableRequired(variable, data)
	if err != nil {
		return err
	}
	if required && stringValue == "" {
		if variable.Flag != "" {
			return fmt.Errorf("missing --%s", variable.Flag)
		}
		return fmt.Errorf("missing %s", variable.Name)
	}

	forbidden, err := conditionMatches(variable.ForbiddenWhen, data, false)
	if err != nil {
		return err
	}
	if forbidden && stringValue != "" {
		if variable.ForbiddenMessage != "" {
			return errors.New(variable.ForbiddenMessage)
		}
		return fmt.Errorf("%s is not allowed in this setup", variable.Name)
	}

	return nil
}

func generateSecurePassword(length int, allowedChars string) (string, error) {
	if length <= 0 {
		return "", errors.New("password length must be positive")
	}
	if allowedChars == "" {
		return "", errors.New("password character set is empty")
	}

	password := make([]byte, length)
	maxIndex := big.NewInt(int64(len(allowedChars)))
	for index := range password {
		randomIndex, err := rand.Int(rand.Reader, maxIndex)
		if err != nil {
			return "", fmt.Errorf("generate secure password: %w", err)
		}
		password[index] = allowedChars[randomIndex.Int64()]
	}

	return string(password), nil
}

func conditionMatches(condition string, data map[string]any, defaultValue bool) (bool, error) {
	if strings.TrimSpace(condition) == "" {
		return defaultValue, nil
	}

	value, err := renderStringTemplate("condition", condition, data)
	if err != nil {
		return false, err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return false, nil
	}

	return parseBool(value)
}

func parseBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean value %q; use true or false", value)
	}
}

func (p prompt) optional(question string, defaultValue string) (string, error) {
	fmt.Fprintf(p.output, "%s [%s]: ", question, defaultValue)
	answer, err := p.line()
	if err != nil {
		return "", err
	}

	if answer == "" {
		return defaultValue, nil
	}

	return answer, nil
}

func (p prompt) required(question string) (string, error) {
	for {
		fmt.Fprintf(p.output, "%s: ", question)
		answer, err := p.line()
		if err != nil {
			return "", err
		}

		if answer != "" {
			return answer, nil
		}

		fmt.Fprintln(p.output, "Please enter a value.")
	}
}

func (p prompt) boolean(question string, defaultValue string, required bool) (string, error) {
	normalizedDefault := ""
	if strings.TrimSpace(defaultValue) != "" {
		parsed, err := parseBool(defaultValue)
		if err != nil {
			return "", err
		}
		normalizedDefault = strconv.FormatBool(parsed)
	}

	for {
		if normalizedDefault == "" {
			fmt.Fprintf(p.output, "%s (true/false): ", question)
		} else {
			fmt.Fprintf(p.output, "%s (true/false) [%s]: ", question, normalizedDefault)
		}

		answer, err := p.line()
		if err != nil {
			return "", err
		}

		if answer == "" {
			if normalizedDefault != "" {
				return normalizedDefault, nil
			}
			if !required {
				return "", nil
			}
			fmt.Fprintln(p.output, "Please answer true or false.")
			continue
		}

		parsed, err := parseBool(answer)
		if err == nil {
			return strconv.FormatBool(parsed), nil
		}

		fmt.Fprintln(p.output, "Please answer true or false.")
	}
}

func (p prompt) line() (string, error) {
	line, err := p.reader.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", err
	}

	return strings.TrimSpace(line), nil
}

func absolutePath(value string, baseDir string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", errors.New("path is empty")
	}

	expanded, err := expandHome(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}

	if !filepath.IsAbs(expanded) {
		if baseDir == "" {
			baseDir, err = os.Getwd()
			if err != nil {
				return "", err
			}
		}
		expanded = filepath.Join(baseDir, expanded)
	}

	return filepath.Abs(expanded)
}

func expandHome(value string) (string, error) {
	if value == "~" || strings.HasPrefix(value, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}

		if value == "~" {
			return homeDir, nil
		}

		return filepath.Join(homeDir, strings.TrimPrefix(value, "~/")), nil
	}

	if strings.HasPrefix(value, "~") {
		return "", errors.New("~user paths are not supported")
	}

	return value, nil
}

func prepareDirectories(directories []pathConfig, data map[string]any) error {
	for _, directory := range directories {
		shouldCreate, err := conditionMatches(directory.When, data, true)
		if err != nil {
			return err
		}
		if !shouldCreate {
			continue
		}

		path, err := renderPath(directory.Path, data)
		if err != nil {
			return err
		}
		mode, err := parseFileMode(directory.Mode, 0o755)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(path, mode); err != nil {
			return fmt.Errorf("create directory %s: %w", path, err)
		}
	}

	return nil
}

func copyConfigAsset(asset assetConfig, data map[string]any, force bool) (copySummary, error) {
	shouldCopy, err := conditionMatches(asset.When, data, true)
	if err != nil {
		return copySummary{}, err
	}
	if !shouldCopy {
		return copySummary{}, nil
	}

	sourceRoot, err := renderProjectPath(asset.Source, data)
	if err != nil {
		return copySummary{}, err
	}
	targetRoot, err := renderPath(asset.Target, data)
	if err != nil {
		return copySummary{}, err
	}

	if sourceRoot == targetRoot {
		return copySummary{}, nil
	}
	if isPathInside(sourceRoot, targetRoot) {
		return copySummary{}, fmt.Errorf("target directory %s must not be inside source directory %s", targetRoot, sourceRoot)
	}

	var summary copySummary
	err = filepath.WalkDir(sourceRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		relativePath, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		if relativePath == "." {
			return nil
		}

		targetPath := filepath.Join(targetRoot, relativePath)
		if entry.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}

		if shouldSkipAsset(entry, asset.Exclude) {
			return nil
		}

		if _, err := os.Stat(targetPath); err == nil && !force {
			summary.Skipped++
			return nil
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}

		if err := copyFile(path, targetPath); err != nil {
			return err
		}
		summary.Copied++
		return nil
	})

	return summary, err
}

func shouldSkipAsset(entry fs.DirEntry, exclude excludeConfig) bool {
	if entry.Type()&os.ModeSymlink != 0 {
		return true
	}
	for _, name := range exclude.Names {
		if entry.Name() == name {
			return true
		}
	}
	for _, suffix := range exclude.Suffixes {
		if strings.HasSuffix(entry.Name(), suffix) {
			return true
		}
	}

	return false
}

func isPathInside(parent string, child string) bool {
	relativePath, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}

	return relativePath != "." && relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(os.PathSeparator))
}

func copyFile(sourcePath string, targetPath string) error {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()

	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer target.Close()

	if _, err := io.Copy(target, source); err != nil {
		return err
	}

	return os.Chmod(targetPath, info.Mode().Perm())
}

func renderEnvFile(env envConfig, data map[string]any) error {
	if env.Output == "" {
		return nil
	}
	if env.Source == "" {
		return errors.New("env.source is required when env.output is set")
	}

	templatePath, err := renderProjectPath(env.Source, data)
	if err != nil {
		return err
	}
	content, err := renderTemplate(templatePath, data)
	if err != nil {
		return err
	}
	path, err := renderPath(env.Output, data)
	if err != nil {
		return err
	}
	mode, err := parseFileMode(env.Mode, defaultEnvFileMode)
	if err != nil {
		return err
	}

	return writeMergedEnvFile(path, content, mode, env, data)
}

func writeMergedEnvFile(path string, renderedContent []byte, mode fs.FileMode, env envConfig, data map[string]any) error {
	existingContent, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return writeFileWithMode(path, renderedContent, mode)
		}

		return err
	}
	if shouldReuseExistingPasswords(data) {
		renderedContent = preserveExistingEnvAssignments(renderedContent, existingContent, passwordProtectedConfig(env.Protected))
	}

	mergedContent := mergeEnvContent(renderedContent, existingContent, env.Protected)

	defaultPath, err := envDefaultPath(path, env, data)
	if err != nil {
		return err
	}
	needsDefault := !bytes.Equal(renderedContent, mergedContent)
	if !needsDefault {
		if err := removeFileIfExists(defaultPath); err != nil {
			return fmt.Errorf("remove default env %s: %w", defaultPath, err)
		}
	}

	if bytes.Equal(existingContent, mergedContent) {
		return nil
	}

	if needsDefault {
		if err := writeFileWithMode(defaultPath, renderedContent, mode); err != nil {
			return fmt.Errorf("write default env %s: %w", defaultPath, err)
		}
	}

	backupPath, err := nextEnvBackupPath(path, env, data)
	if err != nil {
		return err
	}
	if err := writeFileWithMode(backupPath, existingContent, mode); err != nil {
		return fmt.Errorf("write env backup %s: %w", backupPath, err)
	}

	return writeFileWithMode(path, mergedContent, mode)
}

func shouldReuseExistingPasswords(data map[string]any) bool {
	reuse, _ := data["SetupJSONProvided"].(bool)
	return reuse
}

func preserveExistingEnvAssignments(renderedContent []byte, existingContent []byte, protected protectedConfig) []byte {
	existingValues := parseEnvAssignments(existingContent)
	lines := splitLines(string(renderedContent))

	for index, line := range lines {
		key, prefix, ok := parseEnvAssignment(line)
		if !ok || !isProtectedEnvKey(key, protected) {
			continue
		}

		existingValue, found := existingValues[key]
		if !found {
			continue
		}

		lines[index] = prefix + existingValue
	}

	return []byte(strings.Join(lines, "\n"))
}

func passwordProtectedConfig(protected protectedConfig) protectedConfig {
	return protectedConfig{
		Prefixes: filterPasswordSelectors(protected.Prefixes),
		Suffixes: filterPasswordSelectors(protected.Suffixes),
		Keys:     filterPasswordSelectors(protected.Keys),
	}
}

func filterPasswordSelectors(values []string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if strings.Contains(strings.ToUpper(value), "PASSWORD") {
			filtered = append(filtered, value)
		}
	}

	return filtered
}

func removeFileIfExists(path string) error {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return nil
}

func envDefaultPath(path string, env envConfig, data map[string]any) (string, error) {
	if env.DefaultOutput == "" {
		return filepath.Join(filepath.Dir(path), defaultEnvDefaultFile), nil
	}

	return renderPath(env.DefaultOutput, data)
}

func nextEnvBackupPath(path string, env envConfig, data map[string]any) (string, error) {
	dir := filepath.Dir(path)
	timestamp := nowFunc().Format("2006-01-02_15-04-05")
	backupData := cloneData(data)
	backupData["Timestamp"] = timestamp
	backupData["EnvFile"] = filepath.Base(path)

	baseName := env.BackupNameTemplate
	if baseName == "" {
		baseName = "{{ .EnvFile }}_{{ .Timestamp }}"
	}
	baseName, err := renderStringTemplate("env backup name", baseName, backupData)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(dir, baseName)

	if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
		return candidate, nil
	} else if err != nil {
		return "", err
	}

	for index := 1; ; index++ {
		candidate = filepath.Join(dir, fmt.Sprintf("%s_%d", baseName, index))
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
}

func mergeEnvContent(renderedContent []byte, existingContent []byte, protected protectedConfig) []byte {
	existingValues := parseEnvAssignments(existingContent)
	lines := splitLines(string(renderedContent))

	for index, line := range lines {
		key, prefix, ok := parseEnvAssignment(line)
		if !ok || isProtectedEnvKey(key, protected) {
			continue
		}

		existingValue, found := existingValues[key]
		if !found {
			continue
		}

		lines[index] = prefix + existingValue
	}

	return []byte(strings.Join(lines, "\n"))
}

func parseEnvAssignments(content []byte) map[string]string {
	assignments := make(map[string]string)
	for _, line := range splitLines(string(content)) {
		key, _, value, ok := parseEnvAssignmentValue(line)
		if !ok {
			continue
		}

		assignments[key] = value
	}

	return assignments
}

func splitLines(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	return strings.Split(content, "\n")
}

func parseEnvAssignment(line string) (string, string, bool) {
	key, prefix, _, ok := parseEnvAssignmentValue(line)
	return key, prefix, ok
}

func parseEnvAssignmentValue(line string) (string, string, string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", "", false
	}

	separatorIndex := strings.IndexRune(line, '=')
	if separatorIndex < 0 {
		return "", "", "", false
	}

	left := line[:separatorIndex]
	right := line[separatorIndex+1:]
	key := strings.TrimSpace(left)
	if strings.HasPrefix(key, "export ") {
		key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
	}
	if key == "" {
		return "", "", "", false
	}

	return key, left + "=", strings.TrimLeft(right, " \t"), true
}

func isProtectedEnvKey(key string, protected protectedConfig) bool {
	for _, prefix := range protected.Prefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}

	for _, suffix := range protected.Suffixes {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}

	for _, protectedKey := range protected.Keys {
		if key == protectedKey {
			return true
		}
	}

	return false
}

func renderTemplateFiles(targets []templateConfig, data map[string]any) error {
	for _, target := range targets {
		shouldRender, err := conditionMatches(target.When, data, true)
		if err != nil {
			return err
		}
		if !shouldRender {
			continue
		}

		templatePath, err := renderProjectPath(target.Source, data)
		if err != nil {
			return err
		}
		outputPath, err := renderPath(target.Target, data)
		if err != nil {
			return err
		}
		mode, err := parseFileMode(target.Mode, defaultFileMode)
		if err != nil {
			return err
		}

		content, err := renderTemplate(templatePath, data)
		if err != nil {
			return err
		}

		if err := writeFileWithMode(outputPath, content, mode); err != nil {
			return err
		}
	}

	return nil
}

func renderTemplate(templatePath string, data map[string]any) ([]byte, error) {
	content, err := os.ReadFile(templatePath)
	if err != nil {
		return nil, fmt.Errorf("read template %s: %w", templatePath, err)
	}

	tmpl, err := template.New(filepath.Base(templatePath)).Funcs(templateFuncs()).Option("missingkey=error").Parse(string(content))
	if err != nil {
		return nil, fmt.Errorf("parse template %s: %w", templatePath, err)
	}

	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, data); err != nil {
		return nil, fmt.Errorf("render template %s: %w", templatePath, err)
	}

	return rendered.Bytes(), nil
}

func writeStateFile(state stateConfig, data map[string]any) error {
	if state.Output == "" {
		return nil
	}

	statePath, err := renderPath(state.Output, data)
	if err != nil {
		return err
	}
	mode, err := parseFileMode(state.Mode, defaultFileMode)
	if err != nil {
		return err
	}

	stateData := map[string]any{}
	for _, entry := range state.Entries {
		if entry.Key == "" {
			continue
		}

		value, err := stateEntryValue(entry, data)
		if err != nil {
			return err
		}
		if entry.OmitEmpty && isEmptyStateValue(value) {
			continue
		}
		stateData[entry.Key] = value
	}

	content, err := json.MarshalIndent(stateData, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal setup json: %w", err)
	}
	content = append(content, '\n')

	return writeFileWithMode(statePath, content, mode)
}

func stateEntryValue(entry stateEntryConfig, data map[string]any) (any, error) {
	var value any
	if entry.Value != "" {
		rendered, err := renderStringTemplate("state "+entry.Key, entry.Value, data)
		if err != nil {
			return nil, err
		}
		value = rendered
	} else if entry.Variable != "" {
		value = data[entry.Variable]
	} else {
		value = ""
	}

	if entry.Type == "" {
		return value, nil
	}
	return normalizeTypedValue(entry.Type, fmt.Sprint(value), entry.Key, data)
}

func isEmptyStateValue(value any) bool {
	switch typedValue := value.(type) {
	case string:
		return typedValue == ""
	case nil:
		return true
	default:
		return false
	}
}

func printMessages(messages []string, data map[string]any, stdout io.Writer) error {
	for _, message := range messages {
		rendered, err := renderStringTemplate("message", message, data)
		if err != nil {
			return err
		}
		fmt.Fprint(stdout, rendered)
		if !strings.HasSuffix(rendered, "\n") {
			fmt.Fprintln(stdout)
		}
	}

	return nil
}

func renderProjectPath(pathTemplate string, data map[string]any) (string, error) {
	rendered, err := renderStringTemplate("project path", pathTemplate, data)
	if err != nil {
		return "", err
	}
	projectDir, _ := data["ProjectDir"].(string)
	return absolutePath(rendered, projectDir)
}

func renderPath(pathTemplate string, data map[string]any) (string, error) {
	rendered, err := renderStringTemplate("path", pathTemplate, data)
	if err != nil {
		return "", err
	}
	projectDir, _ := data["ProjectDir"].(string)
	return absolutePath(rendered, projectDir)
}

func renderStringTemplate(name string, templateContent string, data map[string]any) (string, error) {
	tmpl, err := template.New(name).Funcs(templateFuncs()).Option("missingkey=error").Parse(templateContent)
	if err != nil {
		return "", err
	}

	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, data); err != nil {
		return "", err
	}

	return rendered.String(), nil
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"lower": strings.ToLower,
		"upper": strings.ToUpper,
		"trim":  strings.TrimSpace,
		"quote": strconv.Quote,
	}
}

func parseFileMode(value string, defaultMode fs.FileMode) (fs.FileMode, error) {
	if strings.TrimSpace(value) == "" {
		return defaultMode, nil
	}

	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 8, 32)
	if err != nil {
		return 0, fmt.Errorf("parse file mode %q: %w", value, err)
	}

	return fs.FileMode(parsed), nil
}

func writeFileWithMode(path string, content []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		return err
	}

	return os.Chmod(path, mode)
}

func cloneData(data map[string]any) map[string]any {
	cloned := make(map[string]any, len(data))
	for key, value := range data {
		cloned[key] = value
	}

	return cloned
}
