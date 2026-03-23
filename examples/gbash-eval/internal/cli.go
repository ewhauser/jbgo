package gbasheval

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

var runCLIExecutor = Run

type RunConfig struct {
	DatasetPath  string
	ProviderName string
	Model        string
	EvalType     string
	Baseline     bool
	MaxTurns     int
	Save         bool
	OutputDir    string
	Moniker      string
	TaskIDs      []string
}

func RunCLI(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	if len(args) == 0 {
		printCLIUsage(stdout)
		return errors.New("missing command")
	}

	switch args[0] {
	case "help", "-h", "--help":
		printCLIUsage(stdout)
		return nil
	case "run":
		cfg, err := parseRunFlags(args[1:], stderr)
		if err != nil {
			return err
		}
		return runCLIExecutor(ctx, cfg, stdout, stderr)
	default:
		printCLIUsage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func parseRunFlags(args []string, stderr io.Writer) (RunConfig, error) {
	cfg := RunConfig{
		EvalType:  "bash",
		MaxTurns:  10,
		OutputDir: DefaultOutputDir(),
	}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	taskIDs := taskIDFlag{}
	fs.StringVar(&cfg.DatasetPath, "dataset", "", "path to JSONL dataset file")
	fs.StringVar(&cfg.ProviderName, "provider", "", "provider: anthropic, openai, or openresponses")
	fs.StringVar(&cfg.Model, "model", "", "model name")
	fs.StringVar(&cfg.EvalType, "eval-type", cfg.EvalType, "eval type: bash or scripting-tool")
	fs.BoolVar(&cfg.Baseline, "baseline", false, "scripting-tool only: expose individual tools instead of a single scripted bash tool")
	fs.IntVar(&cfg.MaxTurns, "max-turns", cfg.MaxTurns, "maximum LLM turns per task")
	fs.BoolVar(&cfg.Save, "save", false, "save JSON and Markdown reports to disk")
	fs.StringVar(&cfg.OutputDir, "output", cfg.OutputDir, "output directory for saved reports")
	fs.StringVar(&cfg.Moniker, "moniker", "", "custom run identifier")
	fs.Var(&taskIDs, "task", "run only specific task ID(s); repeat the flag or pass a comma-separated list")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage: gbash-eval run --dataset PATH --provider NAME --model NAME [options]\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return RunConfig{}, err
	}
	if fs.NArg() != 0 {
		return RunConfig{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	if cfg.DatasetPath == "" {
		return RunConfig{}, errors.New("--dataset is required")
	}
	if cfg.ProviderName == "" {
		return RunConfig{}, errors.New("--provider is required")
	}
	if cfg.Model == "" {
		return RunConfig{}, errors.New("--model is required")
	}
	if cfg.MaxTurns <= 0 {
		return RunConfig{}, errors.New("--max-turns must be positive")
	}
	if cfg.EvalType != "bash" && cfg.EvalType != "scripting-tool" {
		return RunConfig{}, fmt.Errorf("unknown eval type %q", cfg.EvalType)
	}
	if cfg.Baseline && cfg.EvalType != "scripting-tool" {
		return RunConfig{}, errors.New("--baseline is only valid with --eval-type scripting-tool")
	}
	cfg.TaskIDs = taskIDs.Values()
	if cfg.Moniker == "" {
		cfg.Moniker = sanitizeMoniker(cfg.ProviderName + "-" + cfg.Model)
	}
	return cfg, nil
}

func printCLIUsage(w io.Writer) {
	if w == nil {
		w = io.Discard
	}
	_, _ = fmt.Fprintln(w, "Usage:")
	_, _ = fmt.Fprintln(w, "  gbash-eval run --dataset PATH --provider NAME --model NAME [options]")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Commands:")
	_, _ = fmt.Fprintln(w, "  run     Run the evaluator against a dataset")
}

func sanitizeMoniker(value string) string {
	replacer := strings.NewReplacer("/", "-", ":", "-", " ", "-", "\t", "-")
	return replacer.Replace(value)
}

type taskIDFlag struct {
	values []string
	seen   map[string]struct{}
}

func (f *taskIDFlag) String() string {
	return strings.Join(f.values, ",")
}

func (f *taskIDFlag) Set(value string) error {
	for part := range strings.SplitSeq(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if f.seen == nil {
			f.seen = make(map[string]struct{})
		}
		if _, ok := f.seen[part]; ok {
			continue
		}
		f.seen[part] = struct{}{}
		f.values = append(f.values, part)
	}
	return nil
}

func (f *taskIDFlag) Values() []string {
	return append([]string(nil), f.values...)
}
