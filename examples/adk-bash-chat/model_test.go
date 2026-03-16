package main

import "testing"

func TestResolveBackendAutoPrefersGeminiAPIKey(t *testing.T) {
	t.Parallel()
	got, err := resolveBackend(backendAuto, map[string]string{
		"GOOGLE_API_KEY":        "api-key",
		"GOOGLE_CLOUD_PROJECT":  "demo-project",
		"GOOGLE_CLOUD_LOCATION": "us-central1",
	})
	if err != nil {
		t.Fatalf("resolveBackend() error = %v", err)
	}
	if got.mode != backendGemini {
		t.Fatalf("mode = %q, want %q", got.mode, backendGemini)
	}
	if got.apiKey != "api-key" {
		t.Fatalf("apiKey = %q, want %q", got.apiKey, "api-key")
	}
}

func TestResolveBackendAutoUsesVertexWhenConfigured(t *testing.T) {
	t.Parallel()
	got, err := resolveBackend(backendAuto, map[string]string{
		"GOOGLE_CLOUD_PROJECT":      "demo-project",
		"GOOGLE_CLOUD_LOCATION":     "us-central1",
		"GOOGLE_GENAI_USE_VERTEXAI": "true",
	})
	if err != nil {
		t.Fatalf("resolveBackend() error = %v", err)
	}
	if got.mode != backendVertex {
		t.Fatalf("mode = %q, want %q", got.mode, backendVertex)
	}
	if got.project != "demo-project" || got.location != "us-central1" {
		t.Fatalf("resolved backend = %#v", got)
	}
}

func TestResolveBackendExplicitVertexRequiresProjectAndLocation(t *testing.T) {
	t.Parallel()
	_, err := resolveBackend(backendVertex, map[string]string{
		"GOOGLE_CLOUD_PROJECT": "demo-project",
	})
	if err == nil {
		t.Fatal("resolveBackend() error = nil, want error")
	}
}

func TestParseCLIOptionsRejectsUnknownBackend(t *testing.T) {
	t.Parallel()
	_, err := parseCLIOptions([]string{"--backend=other"})
	if err == nil {
		t.Fatal("parseCLIOptions() error = nil, want error")
	}
}
