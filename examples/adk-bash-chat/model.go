package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"google.golang.org/adk/model"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/genai"
)

const defaultModelName = "gemini-2.5-flash"

type backendMode string

const (
	backendAuto   backendMode = "auto"
	backendGemini backendMode = "gemini"
	backendVertex backendMode = "vertex"
)

type cliOptions struct {
	backend   backendMode
	modelName string
}

type resolvedBackend struct {
	mode     backendMode
	apiKey   string
	project  string
	location string
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	opts, err := parseCLIOptions(args)
	if err != nil {
		return err
	}

	modelBackend, err := resolveBackend(opts.backend, map[string]string{
		"GOOGLE_API_KEY":            strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")),
		"GEMINI_API_KEY":            strings.TrimSpace(os.Getenv("GEMINI_API_KEY")),
		"GOOGLE_CLOUD_PROJECT":      strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_PROJECT")),
		"GOOGLE_CLOUD_LOCATION":     strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_LOCATION")),
		"GOOGLE_CLOUD_REGION":       strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_REGION")),
		"GOOGLE_GENAI_USE_VERTEXAI": strings.TrimSpace(os.Getenv("GOOGLE_GENAI_USE_VERTEXAI")),
	})
	if err != nil {
		return err
	}

	llm, err := newModel(ctx, opts.modelName, modelBackend)
	if err != nil {
		return fmt.Errorf("create model: %w", err)
	}

	app, err := newChatApp(ctx, llm, opts.modelName, modelBackend)
	if err != nil {
		return err
	}

	return app.run(ctx, stdin, stdout, stderr)
}

func parseCLIOptions(args []string) (cliOptions, error) {
	opts := cliOptions{
		backend:   backendAuto,
		modelName: defaultModelName,
	}

	fs := flag.NewFlagSet("adk-bash-chat", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var backendValue string
	fs.StringVar(&backendValue, "backend", string(backendAuto), "model backend: auto, gemini, or vertex")
	fs.StringVar(&opts.modelName, "model", defaultModelName, "Gemini model name to use")

	if err := fs.Parse(args); err != nil {
		return cliOptions{}, fmt.Errorf("parse flags: %w", err)
	}

	switch backendMode(strings.ToLower(strings.TrimSpace(backendValue))) {
	case backendAuto:
		opts.backend = backendAuto
	case backendGemini:
		opts.backend = backendGemini
	case backendVertex:
		opts.backend = backendVertex
	default:
		return cliOptions{}, fmt.Errorf("unsupported --backend %q (want auto, gemini, or vertex)", backendValue)
	}

	if strings.TrimSpace(opts.modelName) == "" {
		return cliOptions{}, errors.New("--model must not be empty")
	}

	return opts, nil
}

func resolveBackend(mode backendMode, env map[string]string) (resolvedBackend, error) {
	apiKey := firstNonEmpty(env["GOOGLE_API_KEY"], env["GEMINI_API_KEY"])
	project := strings.TrimSpace(env["GOOGLE_CLOUD_PROJECT"])
	location := firstNonEmpty(env["GOOGLE_CLOUD_LOCATION"], env["GOOGLE_CLOUD_REGION"])
	vertexHint := isTruthy(env["GOOGLE_GENAI_USE_VERTEXAI"])

	switch mode {
	case backendGemini:
		if apiKey == "" {
			return resolvedBackend{}, errors.New("gemini mode requires GOOGLE_API_KEY or GEMINI_API_KEY")
		}
		return resolvedBackend{mode: backendGemini, apiKey: apiKey}, nil
	case backendVertex:
		if project == "" || location == "" {
			return resolvedBackend{}, errors.New("vertex mode requires GOOGLE_CLOUD_PROJECT and GOOGLE_CLOUD_LOCATION or GOOGLE_CLOUD_REGION")
		}
		return resolvedBackend{mode: backendVertex, project: project, location: location}, nil
	case backendAuto:
		if apiKey != "" {
			return resolvedBackend{mode: backendGemini, apiKey: apiKey}, nil
		}
		if project != "" && location != "" && vertexHint {
			return resolvedBackend{mode: backendVertex, project: project, location: location}, nil
		}
		if project != "" && location != "" {
			return resolvedBackend{mode: backendVertex, project: project, location: location}, nil
		}
		return resolvedBackend{}, errors.New("configure either GOOGLE_API_KEY/GEMINI_API_KEY for Gemini API or GOOGLE_CLOUD_PROJECT plus GOOGLE_CLOUD_LOCATION/GOOGLE_CLOUD_REGION for Vertex AI")
	default:
		return resolvedBackend{}, fmt.Errorf("unsupported backend mode %q", mode)
	}
}

func newModel(ctx context.Context, modelName string, cfg resolvedBackend) (model.LLM, error) {
	clientConfig := &genai.ClientConfig{}

	switch cfg.mode {
	case backendGemini:
		clientConfig.APIKey = cfg.apiKey
	case backendVertex:
		clientConfig.Backend = genai.BackendVertexAI
		clientConfig.Project = cfg.project
		clientConfig.Location = cfg.location
	default:
		return nil, fmt.Errorf("unsupported backend mode %q", cfg.mode)
	}

	return gemini.NewModel(ctx, modelName, clientConfig)
}

func isTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
