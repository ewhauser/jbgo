package completionutil

import (
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/ewhauser/gbash/internal/shellstate"
	"github.com/ewhauser/gbash/shell/syntax"
)

var validCompletionOptions = []string{
	"bashdefault",
	"default",
	"dirnames",
	"filenames",
	"noquote",
	"nosort",
	"nospace",
	"plusdirs",
}

var validCompgenActions = []string{
	"alias",
	"builtin",
	"command",
	"directory",
	"export",
	"file",
	"function",
	"helptopic",
	"keyword",
	"setopt",
	"shopt",
	"user",
	"variable",
}

var shellBuiltinNames = []string{
	"[",
	":",
	".",
	"alias",
	"bg",
	"bind",
	"break",
	"builtin",
	"caller",
	"cd",
	"command",
	"compgen",
	"complete",
	"compopt",
	"continue",
	"declare",
	"dirs",
	"disown",
	"echo",
	"enable",
	"eval",
	"exec",
	"exit",
	"export",
	"false",
	"fc",
	"fg",
	"getopts",
	"hash",
	"help",
	"history",
	"jobs",
	"kill",
	"let",
	"local",
	"logout",
	"mapfile",
	"newgrp",
	"popd",
	"printf",
	"pushd",
	"pwd",
	"read",
	"readarray",
	"readonly",
	"return",
	"set",
	"shift",
	"shopt",
	"source",
	"suspend",
	"test",
	"times",
	"trap",
	"true",
	"type",
	"typeset",
	"ulimit",
	"umask",
	"unalias",
	"unset",
	"wait",
}

var shellBuiltinSet = func() map[string]struct{} {
	out := make(map[string]struct{}, len(shellBuiltinNames))
	for _, name := range shellBuiltinNames {
		out[name] = struct{}{}
	}
	return out
}()

var shellKeywordNames = []string{
	"!",
	"[[",
	"]]",
	"case",
	"coproc",
	"do",
	"done",
	"elif",
	"else",
	"esac",
	"fi",
	"for",
	"function",
	"if",
	"in",
	"select",
	"then",
	"time",
	"until",
	"while",
	"{",
	"}",
}

const completeUsageMessage = "complete: usage: complete [-abcdefgjksuv] [-pr] [-DEI] [-o option] [-A action] [-G globpat] [-W wordlist] [-F function] [-C command] [-X filterpat] [-P prefix] [-S suffix] [name ...]"

type Backend interface {
	ValidateWordlistSyntax(wordlist string) error
	ExpandWordlist(wordlist string) ([]string, error)
	FunctionExists(name string) bool
	FunctionNames(prefix string) []string
	EnabledBuiltinNames(prefix string) []string
	VariableNames(prefix string, exportedOnly bool) []string
	AliasNames(prefix string) []string
	SetoptNames(prefix string) []string
	ShoptNames(prefix string) []string
	ExternalCommandNames(prefix string) ([]string, error)
	FileNames(prefix string, dirsOnly bool) ([]string, error)
	UserNames(prefix string) ([]string, error)
	MatchFilterPattern(pattern, candidate string) (bool, error)
	RunFunction(name string, req HookRequest) HookResult
	RunCommandHook(command string, req HookRequest) HookResult
	CompArgv() []string
	SetScalar(name, value string) error
	SetArray(name string, values []string) error
}

type HookRequest struct {
	Word string
}

type HookResult struct {
	Candidates []string
	Status     int
}

type CompleteConfig struct {
	PrintMode   bool
	RemoveMode  bool
	IsDefault   bool
	IsEmptyLine bool
	Wordlist    string
	HasWordlist bool
	Function    string
	HasFunction bool
	Command     string
	HasCommand  bool
	Filter      string
	HasFilter   bool
	Prefix      string
	HasPrefix   bool
	Suffix      string
	HasSuffix   bool
	Options     []string
	Actions     []string
	Commands    []string
}

type CompoptConfig struct {
	IsDefault      bool
	IsEmptyLine    bool
	EnableOptions  []string
	DisableOptions []string
	Commands       []string
}

type CompgenConfig struct {
	Actions     []string
	Options     []string
	Wordlist    string
	HasWordlist bool
	Function    string
	HasFunction bool
	Command     string
	HasCommand  bool
	Filter      string
	HasFilter   bool
	Prefix      string
	HasPrefix   bool
	Suffix      string
	HasSuffix   bool
	Word        string
	HasWord     bool
}

type UsageError struct {
	Message string
}

func (e *UsageError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func IsBuiltinName(name string) bool {
	_, ok := shellBuiltinSet[name]
	return ok
}

func BuiltinNames(prefix string) []string {
	return filterNames(shellBuiltinNames, prefix)
}

func KeywordNames(prefix string) []string {
	return filterNames(shellKeywordNames, prefix)
}

func IsValidCompletionOption(name string) bool {
	return slices.Contains(validCompletionOptions, strings.TrimSpace(name))
}

func MergeCompletionOptions(current, enable, disable []string) []string {
	order := make([]string, 0, len(current)+len(enable))
	seen := make(map[string]struct{}, len(current)+len(enable))
	for _, opt := range current {
		if _, ok := seen[opt]; ok {
			continue
		}
		seen[opt] = struct{}{}
		order = append(order, opt)
	}
	for _, opt := range enable {
		if _, ok := seen[opt]; ok {
			continue
		}
		seen[opt] = struct{}{}
		order = append(order, opt)
	}
	if len(order) == 0 {
		return nil
	}
	disabled := make(map[string]struct{}, len(disable))
	for _, opt := range disable {
		disabled[opt] = struct{}{}
	}
	out := make([]string, 0, len(order))
	for _, opt := range order {
		if _, ok := disabled[opt]; ok {
			continue
		}
		out = append(out, opt)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func QuoteArgument(arg string) string {
	if !strings.ContainsAny(arg, " '") {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

func FormatCompleteSpec(name string, spec *shellstate.CompletionSpec) string {
	var b strings.Builder
	b.WriteString("complete")
	for _, opt := range spec.Options {
		fmt.Fprintf(&b, " -o %s", opt)
	}
	for _, action := range spec.Actions {
		fmt.Fprintf(&b, " -A %s", action)
	}
	if spec.HasWordlist {
		fmt.Fprintf(&b, " -W %s", QuoteArgument(spec.Wordlist))
	}
	if spec.HasFunction {
		fmt.Fprintf(&b, " -F %s", spec.Function)
	}
	if spec.HasCommand {
		fmt.Fprintf(&b, " -C %s", QuoteArgument(spec.Command))
	}
	if spec.HasFilter {
		fmt.Fprintf(&b, " -X %s", QuoteArgument(spec.Filter))
	}
	if spec.HasPrefix {
		fmt.Fprintf(&b, " -P %s", QuoteArgument(spec.Prefix))
	}
	if spec.HasSuffix {
		fmt.Fprintf(&b, " -S %s", QuoteArgument(spec.Suffix))
	}
	switch name {
	case shellstate.CompletionSpecDefaultKey:
		b.WriteString(" -D")
	case shellstate.CompletionSpecEmptyKey:
		b.WriteString(" -E")
	default:
		fmt.Fprintf(&b, " %s", name)
	}
	return b.String()
}

func IsInternalCompletionSpec(name string) bool {
	switch name {
	case shellstate.CompletionSpecDefaultKey, shellstate.CompletionSpecEmptyKey:
		return true
	default:
		return false
	}
}

func CompletionSpecFromConfig(cfg *CompleteConfig) shellstate.CompletionSpec {
	spec := shellstate.CompletionSpec{
		IsDefault:   cfg != nil && cfg.IsDefault,
		IsEmptyLine: cfg != nil && cfg.IsEmptyLine,
	}
	if cfg == nil {
		return spec
	}
	if cfg.HasWordlist {
		spec.Wordlist = cfg.Wordlist
		spec.HasWordlist = true
	}
	if cfg.HasFunction {
		spec.Function = cfg.Function
		spec.HasFunction = true
	}
	if cfg.HasCommand {
		spec.Command = cfg.Command
		spec.HasCommand = true
	}
	if cfg.HasFilter {
		spec.Filter = cfg.Filter
		spec.HasFilter = true
	}
	if cfg.HasPrefix {
		spec.Prefix = cfg.Prefix
		spec.HasPrefix = true
	}
	if cfg.HasSuffix {
		spec.Suffix = cfg.Suffix
		spec.HasSuffix = true
	}
	if len(cfg.Options) > 0 {
		spec.Options = append(spec.Options, cfg.Options...)
	}
	if len(cfg.Actions) > 0 {
		spec.Actions = append(spec.Actions, cfg.Actions...)
	}
	return spec
}

func ParseCompleteArgs(args []string) (*CompleteConfig, error) {
	cfg := &CompleteConfig{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-p":
			cfg.PrintMode = true
		case "-r":
			cfg.RemoveMode = true
		case "-D":
			cfg.IsDefault = true
		case "-E":
			cfg.IsEmptyLine = true
		case "-W":
			i++
			if i >= len(args) {
				return nil, &UsageError{Message: "complete: -W: option requires an argument"}
			}
			cfg.Wordlist = args[i]
			cfg.HasWordlist = true
		case "-F":
			i++
			if i >= len(args) {
				return nil, &UsageError{Message: "complete: -F: option requires an argument"}
			}
			cfg.Function = args[i]
			cfg.HasFunction = true
		case "-C":
			i++
			if i >= len(args) {
				return nil, &UsageError{Message: "complete: -C: option requires an argument"}
			}
			cfg.Command = args[i]
			cfg.HasCommand = true
		case "-X":
			i++
			if i >= len(args) {
				return nil, &UsageError{Message: "complete: -X: option requires an argument"}
			}
			cfg.Filter = args[i]
			cfg.HasFilter = true
		case "-P":
			i++
			if i >= len(args) {
				return nil, &UsageError{Message: "complete: -P: option requires an argument"}
			}
			cfg.Prefix = args[i]
			cfg.HasPrefix = true
		case "-S":
			i++
			if i >= len(args) {
				return nil, &UsageError{Message: "complete: -S: option requires an argument"}
			}
			cfg.Suffix = args[i]
			cfg.HasSuffix = true
		case "-o":
			i++
			if i >= len(args) {
				return nil, &UsageError{Message: "complete: -o: option requires an argument"}
			}
			opt := args[i]
			if !IsValidCompletionOption(opt) {
				return nil, &UsageError{Message: fmt.Sprintf("complete: %s: invalid option name", opt)}
			}
			cfg.Options = append(cfg.Options, opt)
		case "-A":
			i++
			if i >= len(args) {
				return nil, &UsageError{Message: "complete: -A: option requires an argument"}
			}
			cfg.Actions = append(cfg.Actions, args[i])
		case "-G":
			i++
			if i >= len(args) {
				return nil, &UsageError{Message: "complete: -G: option requires an argument"}
			}
		case "--":
			cfg.Commands = append(cfg.Commands, args[i+1:]...)
			return cfg, nil
		default:
			if arg != "" && arg[0] != '-' {
				cfg.Commands = append(cfg.Commands, arg)
			}
		}
	}
	return cfg, nil
}

func ParseCompoptArgs(args []string) (*CompoptConfig, error) {
	cfg := &CompoptConfig{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-D":
			cfg.IsDefault = true
		case "-E":
			cfg.IsEmptyLine = true
		case "-o":
			i++
			if i >= len(args) {
				return nil, &UsageError{Message: "compopt: -o: option requires an argument"}
			}
			opt := args[i]
			if !IsValidCompletionOption(opt) {
				return nil, &UsageError{Message: fmt.Sprintf("compopt: %s: invalid option name", opt)}
			}
			cfg.EnableOptions = append(cfg.EnableOptions, opt)
		case "+o":
			i++
			if i >= len(args) {
				return nil, &UsageError{Message: "compopt: +o: option requires an argument"}
			}
			opt := args[i]
			if !IsValidCompletionOption(opt) {
				return nil, &UsageError{Message: fmt.Sprintf("compopt: %s: invalid option name", opt)}
			}
			cfg.DisableOptions = append(cfg.DisableOptions, opt)
		case "--":
			cfg.Commands = append(cfg.Commands, args[i+1:]...)
			return cfg, nil
		default:
			if arg != "" && arg[0] != '-' && arg[0] != '+' {
				cfg.Commands = append(cfg.Commands, arg)
			}
		}
	}
	return cfg, nil
}

func ParseCompgenArgs(args []string) (*CompgenConfig, error) {
	cfg := &CompgenConfig{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-f":
			cfg.Actions = append(cfg.Actions, "file")
		case "-v":
			cfg.Actions = append(cfg.Actions, "variable")
		case "-e":
			cfg.Actions = append(cfg.Actions, "export")
		case "-k":
			cfg.Actions = append(cfg.Actions, "keyword")
		case "-A":
			i++
			if i >= len(args) {
				return nil, &UsageError{Message: "compgen: -A: option requires an argument"}
			}
			action := args[i]
			if !slices.Contains(validCompgenActions, action) {
				return nil, &UsageError{Message: fmt.Sprintf("compgen: %s: invalid action name", action)}
			}
			cfg.Actions = append(cfg.Actions, action)
		case "-W":
			i++
			if i >= len(args) {
				return nil, &UsageError{Message: "compgen: -W: option requires an argument"}
			}
			cfg.Wordlist = args[i]
			cfg.HasWordlist = true
		case "-F":
			i++
			if i >= len(args) {
				return nil, &UsageError{Message: "compgen: -F: option requires an argument"}
			}
			cfg.Function = args[i]
			cfg.HasFunction = true
		case "-C":
			i++
			if i >= len(args) {
				return nil, &UsageError{Message: "compgen: -C: option requires an argument"}
			}
			cfg.Command = args[i]
			cfg.HasCommand = true
		case "-X":
			i++
			if i >= len(args) {
				return nil, &UsageError{Message: "compgen: -X: option requires an argument"}
			}
			cfg.Filter = args[i]
			cfg.HasFilter = true
		case "-P":
			i++
			if i >= len(args) {
				return nil, &UsageError{Message: "compgen: -P: option requires an argument"}
			}
			cfg.Prefix = args[i]
			cfg.HasPrefix = true
		case "-S":
			i++
			if i >= len(args) {
				return nil, &UsageError{Message: "compgen: -S: option requires an argument"}
			}
			cfg.Suffix = args[i]
			cfg.HasSuffix = true
		case "-o":
			i++
			if i >= len(args) {
				return nil, &UsageError{Message: "compgen: -o: option requires an argument"}
			}
			opt := args[i]
			if !IsValidCompletionOption(opt) {
				return nil, &UsageError{Message: fmt.Sprintf("compgen: %s: invalid option name", opt)}
			}
			cfg.Options = append(cfg.Options, opt)
		case "--":
			if i+1 < len(args) {
				cfg.Word = args[i+1]
				cfg.HasWord = true
			}
			return cfg, nil
		default:
			if arg != "" && arg[0] == '-' {
				return nil, &UsageError{Message: fmt.Sprintf("compgen: invalid option %q", arg)}
			}
			if !cfg.HasWord {
				cfg.Word = arg
				cfg.HasWord = true
			}
		}
	}
	return cfg, nil
}

func ApplyComplete(state *shellstate.CompletionState, backend Backend, cfg *CompleteConfig) ([]string, error) {
	if state == nil {
		state = shellstate.NewCompletionState()
	}
	if cfg == nil {
		cfg = &CompleteConfig{}
	}
	if cfg.RemoveMode {
		if len(cfg.Commands) == 0 && !cfg.IsDefault && !cfg.IsEmptyLine {
			state.Clear()
			return nil, nil
		}
		if cfg.IsDefault {
			state.Delete(shellstate.CompletionSpecDefaultKey)
		}
		if cfg.IsEmptyLine {
			state.Delete(shellstate.CompletionSpecEmptyKey)
		}
		for _, name := range cfg.Commands {
			state.Delete(name)
		}
		return nil, nil
	}
	if cfg.PrintMode {
		return PrintCompletionSpecs(state, cfg.Commands)
	}
	if len(cfg.Commands) == 0 && !cfg.IsDefault && !cfg.IsEmptyLine {
		if !cfg.HasWordlist && !cfg.HasFunction && !cfg.HasCommand && !cfg.HasFilter &&
			!cfg.HasPrefix && !cfg.HasSuffix && len(cfg.Options) == 0 && len(cfg.Actions) == 0 {
			return PrintCompletionSpecs(state, nil)
		}
	}
	if cfg.HasFunction && len(cfg.Commands) == 0 && !cfg.IsDefault && !cfg.IsEmptyLine {
		return nil, &UsageError{Message: completeUsageMessage}
	}
	spec := CompletionSpecFromConfig(cfg)
	if cfg.IsDefault {
		state.Set(shellstate.CompletionSpecDefaultKey, &spec)
	}
	if cfg.IsEmptyLine {
		state.Set(shellstate.CompletionSpecEmptyKey, &spec)
	}
	for _, name := range cfg.Commands {
		state.Set(name, &spec)
	}
	return nil, nil
}

func PrintCompletionSpecs(state *shellstate.CompletionState, commands []string) ([]string, error) {
	if state == nil {
		state = shellstate.NewCompletionState()
	}
	if len(commands) == 0 {
		lines := make([]string, 0, len(state.Keys()))
		for _, name := range state.Keys() {
			if IsInternalCompletionSpec(name) {
				continue
			}
			spec, ok := state.Get(name)
			if !ok {
				continue
			}
			lines = append(lines, FormatCompleteSpec(name, &spec))
		}
		return lines, nil
	}
	lines := make([]string, 0, len(commands))
	for _, name := range commands {
		spec, ok := state.Get(name)
		if !ok {
			return nil, fmt.Errorf("complete: %s: no completion specification", name)
		}
		lines = append(lines, FormatCompleteSpec(name, &spec))
	}
	return lines, nil
}

func ApplyCompopt(state *shellstate.CompletionState, cfg *CompoptConfig) error {
	if state == nil {
		state = shellstate.NewCompletionState()
	}
	if cfg == nil {
		cfg = &CompoptConfig{}
	}
	switch {
	case cfg.IsDefault:
		state.Update(shellstate.CompletionSpecDefaultKey, func(spec *shellstate.CompletionSpec) {
			spec.IsDefault = true
			spec.Options = MergeCompletionOptions(spec.Options, cfg.EnableOptions, cfg.DisableOptions)
		})
		return nil
	case cfg.IsEmptyLine:
		state.Update(shellstate.CompletionSpecEmptyKey, func(spec *shellstate.CompletionSpec) {
			spec.IsEmptyLine = true
			spec.Options = MergeCompletionOptions(spec.Options, cfg.EnableOptions, cfg.DisableOptions)
		})
		return nil
	case len(cfg.Commands) > 0:
		for _, name := range cfg.Commands {
			state.Update(name, func(spec *shellstate.CompletionSpec) {
				spec.Options = MergeCompletionOptions(spec.Options, cfg.EnableOptions, cfg.DisableOptions)
			})
		}
		return nil
	default:
		return fmt.Errorf("compopt: not currently executing completion function")
	}
}

func GenerateCompgen(backend Backend, cfg *CompgenConfig) ([]string, int, error) {
	if cfg == nil {
		cfg = &CompgenConfig{}
	}
	if backend == nil {
		return nil, 1, nil
	}
	var matches []string
	for _, action := range cfg.Actions {
		actionMatches, status := generateActionMatches(backend, action, cfg.Word)
		if status != 0 && len(actionMatches) == 0 {
			continue
		}
		matches = append(matches, actionMatches...)
	}
	if cfg.HasWordlist {
		words, wordlistErr := backend.ExpandWordlist(cfg.Wordlist)
		if wordlistErr == nil {
			matches = append(matches, filterPrefix(words, cfg.Word)...)
		} else {
			status := 1
			return nil, status, nil
		}
	}
	status := 0
	if cfg.HasFunction {
		result := backend.RunFunction(cfg.Function, HookRequest{Word: cfg.Word})
		matches = append(matches, result.Candidates...)
		if result.Status != 0 {
			status = result.Status
		}
	}
	if cfg.HasCommand {
		result := backend.RunCommandHook(cfg.Command, HookRequest{Word: cfg.Word})
		matches = append(matches, result.Candidates...)
		if result.Status != 0 {
			status = result.Status
		}
	}
	if cfg.HasFilter {
		filtered, filterErr := applyFilter(backend, cfg.Filter, matches)
		if filterErr == nil {
			matches = filtered
		} else {
			status := 1
			return nil, status, nil
		}
	}
	if slices.Contains(cfg.Options, "plusdirs") {
		dirs, _ := backend.FileNames(cfg.Word, true)
		matches = append(matches, dirs...)
	}
	if len(matches) == 0 && slices.Contains(cfg.Options, "dirnames") {
		dirs, _ := backend.FileNames(cfg.Word, true)
		matches = append(matches, dirs...)
	}
	if len(matches) == 0 && slices.Contains(cfg.Options, "default") {
		files, _ := backend.FileNames(cfg.Word, false)
		matches = append(matches, files...)
	}
	if cfg.HasPrefix || cfg.HasSuffix {
		for i, match := range matches {
			if cfg.HasPrefix {
				match = cfg.Prefix + match
			}
			if cfg.HasSuffix {
				match += cfg.Suffix
			}
			matches[i] = match
		}
	}
	if len(matches) == 0 {
		if status == 0 {
			status = 1
		}
		return nil, status, nil
	}
	return matches, status, nil
}

func ApplyCompadjust(backend Backend, args []string) error {
	if backend == nil {
		return nil
	}
	values := backend.CompArgv()
	switch len(args) {
	case 1:
		return backend.SetArray(args[0], values)
	case 4:
		cur := ""
		prev := ""
		cword := -1
		if len(values) > 0 {
			cur = values[len(values)-1]
			cword = len(values) - 1
		}
		if len(values) > 1 {
			prev = values[len(values)-2]
		}
		if err := backend.SetScalar(args[0], cur); err != nil {
			return err
		}
		if err := backend.SetScalar(args[1], prev); err != nil {
			return err
		}
		if err := backend.SetArray(args[2], values); err != nil {
			return err
		}
		return backend.SetScalar(args[3], strconv.Itoa(cword))
	default:
		return &UsageError{Message: "compadjust: usage: compadjust words | compadjust cur prev words cword"}
	}
}

func generateActionMatches(backend Backend, action, prefix string) ([]string, int) {
	switch action {
	case "alias":
		return backend.AliasNames(prefix), 0
	case "builtin":
		return BuiltinNames(prefix), 0
	case "command":
		var matches []string
		matches = append(matches, backend.AliasNames(prefix)...)
		matches = append(matches, backend.FunctionNames(prefix)...)
		matches = append(matches, backend.EnabledBuiltinNames(prefix)...)
		matches = append(matches, KeywordNames(prefix)...)
		external, _ := backend.ExternalCommandNames(prefix)
		matches = append(matches, external...)
		return matches, 0
	case "directory":
		matches, _ := backend.FileNames(prefix, true)
		return matches, 0
	case "file":
		matches, _ := backend.FileNames(prefix, false)
		if len(matches) == 0 {
			return nil, 1
		}
		return matches, 0
	case "function":
		return backend.FunctionNames(prefix), 0
	case "helptopic":
		return BuiltinNames(prefix), 0
	case "setopt":
		return backend.SetoptNames(prefix), 0
	case "shopt":
		return backend.ShoptNames(prefix), 0
	case "user":
		matches, _ := backend.UserNames(prefix)
		return matches, 0
	case "variable":
		return backend.VariableNames(prefix, false), 0
	case "export":
		return backend.VariableNames(prefix, true), 0
	case "keyword":
		return KeywordNames(prefix), 0
	default:
		return nil, 1
	}
}

func applyFilter(backend Backend, filter string, matches []string) ([]string, error) {
	negated := strings.HasPrefix(filter, "!")
	pattern := filter
	if negated {
		pattern = strings.TrimPrefix(filter, "!")
	}
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		ok, err := backend.MatchFilterPattern(pattern, match)
		if err != nil {
			return nil, err
		}
		remove := ok
		if negated {
			remove = !ok
		}
		if remove {
			continue
		}
		out = append(out, match)
	}
	return out, nil
}

func filterPrefix(values []string, prefix string) []string {
	if prefix == "" {
		return append([]string(nil), values...)
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			out = append(out, value)
		}
	}
	return out
}

func filterNames(values []string, prefix string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if prefix == "" || strings.HasPrefix(value, prefix) {
			out = append(out, value)
		}
	}
	slices.Sort(out)
	return out
}

func SplitCompletionPath(prefix string) (dirPart, base, searchDir string) {
	dirPart, base = path.Split(prefix)
	switch dirPart {
	case "":
		searchDir = "."
	case "/":
		searchDir = "/"
	default:
		searchDir = strings.TrimSuffix(dirPart, "/")
	}
	return dirPart, base, searchDir
}

func ParseWordlistDocument(src string) (*syntax.Word, error) {
	return ParseWordlistDocumentVariant(src, syntax.LangBash)
}

func ParseWordlistDocumentVariant(src string, lang syntax.LangVariant) (*syntax.Word, error) {
	if src == "" {
		return nil, nil
	}
	if lang == 0 || lang == syntax.LangAuto {
		lang = syntax.LangBash
	}
	parser := syntax.NewParser(syntax.Variant(lang))
	return parser.Document(strings.NewReader(src))
}

func WordlistErrorText(src string, err error) string {
	if err == nil {
		return ""
	}
	if strings.Contains(src, "${") && strings.Contains(err.Error(), "invalid parameter name") {
		return "${: bad substitution"
	}
	return err.Error()
}
