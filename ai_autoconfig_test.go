package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDetectCloudProvidersFindsConfiguredKey(t *testing.T) {
	for _, key := range allCloudProviderEnvKeys() {
		t.Setenv(key, "")
	}
	t.Setenv("MISTRAL_API_KEY", "mistral-secret")

	detected := detectCloudProviders()
	if len(detected) != 1 {
		t.Fatalf("expected exactly one detected provider, got %d: %+v", len(detected), detected)
	}
	if detected[0].Name != "Mistral" {
		t.Fatalf("detected provider = %q, want Mistral", detected[0].Name)
	}
	if detected[0].AuthToken != "mistral-secret" {
		t.Fatalf("detected auth token = %q, want mistral-secret", detected[0].AuthToken)
	}
	if detected[0].Endpoint != "https://api.mistral.ai/v1" {
		t.Fatalf("detected endpoint = %q, want https://api.mistral.ai/v1", detected[0].Endpoint)
	}
}

func TestDetectCloudProvidersGeminiAcceptsEitherKey(t *testing.T) {
	for _, key := range allCloudProviderEnvKeys() {
		t.Setenv(key, "")
	}
	t.Setenv("GOOGLE_API_KEY", "google-secret")

	detected := detectCloudProviders()
	if len(detected) != 1 || detected[0].Name != "Google Gemini" {
		t.Fatalf("expected Google Gemini detection via GOOGLE_API_KEY, got %+v", detected)
	}
}

func TestDetectCloudProvidersAzureRequiresEndpoint(t *testing.T) {
	for _, key := range allCloudProviderEnvKeys() {
		t.Setenv(key, "")
	}
	t.Setenv("AZURE_OPENAI_API_KEY", "azure-secret")
	t.Setenv("AZURE_OPENAI_ENDPOINT", "")

	if detected := detectCloudProviders(); len(detected) != 0 {
		t.Fatalf("expected no detection without AZURE_OPENAI_ENDPOINT, got %+v", detected)
	}

	t.Setenv("AZURE_OPENAI_ENDPOINT", "https://my-resource.openai.azure.com/")
	detected := detectCloudProviders()
	if len(detected) != 1 || detected[0].Endpoint != "https://my-resource.openai.azure.com/" {
		t.Fatalf("expected Azure OpenAI detection with configured endpoint, got %+v", detected)
	}
}

func TestDetectCloudProvidersMultipleKeysPreservesCatalogOrder(t *testing.T) {
	for _, key := range allCloudProviderEnvKeys() {
		t.Setenv(key, "")
	}
	t.Setenv("GROQ_API_KEY", "groq-secret")
	t.Setenv("OPENAI_API_KEY", "openai-secret")

	detected := detectCloudProviders()
	if len(detected) != 2 {
		t.Fatalf("expected 2 detected providers, got %d: %+v", len(detected), detected)
	}
	if detected[0].Name != "OpenAI" || detected[1].Name != "Groq" {
		t.Fatalf("expected catalog order OpenAI, Groq; got %+v", detected)
	}
}

// allCloudProviderEnvKeys returns every env key referenced by the cloud provider catalog, for test isolation.
func allCloudProviderEnvKeys() []string {
	var keys []string
	for _, spec := range cloudProviderCatalog {
		keys = append(keys, spec.EnvKeys...)
		if spec.EndpointEnvKey != "" {
			keys = append(keys, spec.EndpointEnvKey)
		}
	}
	return keys
}

func TestFetchAvailableModelsOllamaFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(ollamaTagsResponse{Models: []struct {
			Name string `json:"name"`
		}{{Name: "llama3.1"}, {Name: "qwen2.5"}}})
	}))
	defer server.Close()

	models, err := fetchAvailableModels(providerOllama, server.URL, "")
	if err != nil {
		t.Fatalf("fetchAvailableModels returned error: %v", err)
	}
	if len(models) != 2 || models[0] != "llama3.1" || models[1] != "qwen2.5" {
		t.Fatalf("fetchAvailableModels returned %+v", models)
	}
}

func TestFetchAvailableModelsOpenAIFormatSendsBearerAuth(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(openAIModelListResponse{Data: []struct {
			ID string `json:"id"`
		}{{ID: "gpt-4.1"}}})
	}))
	defer server.Close()

	models, err := fetchAvailableModels(providerOpenAI, server.URL, "secret-token")
	if err != nil {
		t.Fatalf("fetchAvailableModels returned error: %v", err)
	}
	if len(models) != 1 || models[0] != "gpt-4.1" {
		t.Fatalf("fetchAvailableModels returned %+v", models)
	}
	if gotAuth != "Bearer secret-token" {
		t.Fatalf("Authorization header = %q, want Bearer secret-token", gotAuth)
	}
}

func TestFetchAvailableModelsAnthropicSendsAPIKeyHeader(t *testing.T) {
	var gotKey, gotVersion string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		_ = json.NewEncoder(w).Encode(openAIModelListResponse{Data: []struct {
			ID string `json:"id"`
		}{{ID: "claude-sonnet"}}})
	}))
	defer server.Close()

	models, err := fetchAvailableModels(providerAnthropic, server.URL, "anthropic-secret")
	if err != nil {
		t.Fatalf("fetchAvailableModels returned error: %v", err)
	}
	if len(models) != 1 || models[0] != "claude-sonnet" {
		t.Fatalf("fetchAvailableModels returned %+v", models)
	}
	if gotKey != "anthropic-secret" {
		t.Fatalf("x-api-key header = %q, want anthropic-secret", gotKey)
	}
	if gotVersion == "" {
		t.Fatalf("expected anthropic-version header to be set")
	}
}

func TestFetchAvailableModelsErrorsOnNonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	if _, err := fetchAvailableModels(providerOpenAI, server.URL, "bad-token"); err == nil {
		t.Fatalf("expected error for non-200 response")
	}
}

func TestPromptSelectModelNumericChoice(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("2\n"))
	var stdout bytes.Buffer

	got, err := promptSelectModel(reader, &stdout, []string{"model-a", "model-b"})
	if err != nil {
		t.Fatalf("promptSelectModel returned error: %v", err)
	}
	if got != "model-b" {
		t.Fatalf("promptSelectModel = %q, want model-b", got)
	}
}

func TestResolveOllamaCloudSelectionPromptsForKeyAndSwitchesEndpoint(t *testing.T) {
	t.Setenv("OLLAMA_API_KEY", "")
	reader := bufio.NewReader(strings.NewReader("cloud-key\n"))
	var stdout bytes.Buffer

	endpoint, token, err := resolveOllamaCloudSelection(reader, &stdout, "http://localhost:11434", "localhost", "gemma4-31b:cloud", "")
	if err != nil {
		t.Fatalf("resolveOllamaCloudSelection returned error: %v", err)
	}
	if endpoint != ollamaCloudEndpoint {
		t.Fatalf("endpoint = %q, want %q", endpoint, ollamaCloudEndpoint)
	}
	if token != "cloud-key" {
		t.Fatalf("token = %q, want cloud-key", token)
	}
}

func TestResolveOllamaCloudSelectionUsesExistingKey(t *testing.T) {
	t.Setenv("OLLAMA_API_KEY", "env-key")
	reader := bufio.NewReader(strings.NewReader(""))
	var stdout bytes.Buffer

	endpoint, token, err := resolveOllamaCloudSelection(reader, &stdout, "http://localhost:11434", "localhost", "gemma4-31b:cloud", "")
	if err != nil {
		t.Fatalf("resolveOllamaCloudSelection returned error: %v", err)
	}
	if endpoint != ollamaCloudEndpoint || token != "env-key" {
		t.Fatalf("got endpoint=%q token=%q", endpoint, token)
	}
}

func TestResolveOllamaCloudSelectionLeavesLocalModelsAlone(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader(""))
	var stdout bytes.Buffer

	endpoint, token, err := resolveOllamaCloudSelection(reader, &stdout, "http://localhost:11434", "localhost", "llama3.1:8b", "")
	if err != nil {
		t.Fatalf("resolveOllamaCloudSelection returned error: %v", err)
	}
	if endpoint != "http://localhost:11434" || token != "" {
		t.Fatalf("got endpoint=%q token=%q", endpoint, token)
	}
}

func TestPromptSelectModelCustomEntry(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("3\nmy-custom-model\n"))
	var stdout bytes.Buffer

	got, err := promptSelectModel(reader, &stdout, []string{"model-a", "model-b"})
	if err != nil {
		t.Fatalf("promptSelectModel returned error: %v", err)
	}
	if got != "my-custom-model" {
		t.Fatalf("promptSelectModel = %q, want my-custom-model", got)
	}
}

func TestPromptModelForEndpointFallsBackToFreeTextOnFetchError(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("typed-model\n"))
	var stdout bytes.Buffer

	got, err := promptModelForEndpoint(reader, &stdout, providerOpenAI, "http://127.0.0.1:1", "")
	if err != nil {
		t.Fatalf("promptModelForEndpoint returned error: %v", err)
	}
	if got != "typed-model" {
		t.Fatalf("promptModelForEndpoint = %q, want typed-model", got)
	}
}

func TestPromptInstallEnvValuesAutoUsesDetectedCloudProvider(t *testing.T) {
	for _, key := range allCloudProviderEnvKeys() {
		t.Setenv(key, "")
	}
	// httptest servers listen on loopback, so the endpoint resolves to the native Ollama
	// provider (detectAIProvider treats loopback hosts as local); serve that response shape.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ollamaTagsResponse{Models: []struct {
			Name string `json:"name"`
		}{{Name: "test-model"}}})
	}))
	defer server.Close()

	oldOpenAI := cloudProviderCatalog[0]
	cloudProviderCatalog[0] = cloudProviderSpec{Name: "OpenAI", EnvKeys: []string{"OPENAI_API_KEY"}, Endpoint: server.URL}
	t.Cleanup(func() { cloudProviderCatalog[0] = oldOpenAI })
	t.Setenv("OPENAI_API_KEY", "openai-secret")

	reader := bufio.NewReader(strings.NewReader("1\n"))
	var stdout bytes.Buffer

	values, err := promptInstallEnvValuesAuto(reader, &stdout)
	if err != nil {
		t.Fatalf("promptInstallEnvValuesAuto returned error: %v", err)
	}
	if values == nil {
		t.Fatalf("expected auto-detected values, got nil")
	}
	if values[aiEnvEndpoint] != server.URL {
		t.Fatalf("AI_ENDPOINT = %q, want %q", values[aiEnvEndpoint], server.URL)
	}
	if values[aiEnvAuthToken] != "openai-secret" {
		t.Fatalf("AI_AUTH_TOKEN = %q, want openai-secret", values[aiEnvAuthToken])
	}
	if values[aiEnvModel] != "test-model" {
		t.Fatalf("AI_MODEL = %q, want test-model", values[aiEnvModel])
	}
}

func TestPromptInstallEnvValuesAutoReturnsNilWhenNothingDetected(t *testing.T) {
	for _, key := range allCloudProviderEnvKeys() {
		t.Setenv(key, "")
	}
	// Point every local service candidate at an address nothing listens on so detection finds none.
	oldLocalServices := localAIServices
	localAIServices = []localAIService{
		{Name: "unreachable", BaseURL: "http://127.0.0.1:1", ModelsPath: "/v1/models"},
	}
	t.Cleanup(func() { localAIServices = oldLocalServices })

	reader := bufio.NewReader(strings.NewReader(""))
	var stdout bytes.Buffer

	values, err := promptInstallEnvValuesAuto(reader, &stdout)
	if err != nil {
		t.Fatalf("promptInstallEnvValuesAuto returned error: %v", err)
	}
	if values != nil {
		t.Fatalf("expected nil values when nothing detected, got %+v", values)
	}
}
