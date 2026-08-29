package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// probeTimeout bounds local service discovery so a closed/filtered port cannot stall install.
const probeTimeout = 300 * time.Millisecond

// modelListTimeout bounds the remote model-listing call made against a chosen provider.
const modelListTimeout = 8 * time.Second

// cloudProviderSpec describes a cloud AI provider that can be detected from environment variables.
type cloudProviderSpec struct {
	Name           string
	EnvKeys        []string
	Endpoint       string
	EndpointEnvKey string
}

// cloudProviderCatalog lists the supported cloud providers in display order. AWS Bedrock is
// intentionally excluded; see https://github.com/Jonconradt/ash/issues/4.
var cloudProviderCatalog = []cloudProviderSpec{
	{Name: "OpenAI", EnvKeys: []string{"OPENAI_API_KEY"}, Endpoint: "https://api.openai.com/v1"},
	{Name: "Anthropic", EnvKeys: []string{"ANTHROPIC_API_KEY"}, Endpoint: "https://api.anthropic.com/v1"},
	{Name: "Google Gemini", EnvKeys: []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}, Endpoint: "https://generativelanguage.googleapis.com/v1beta/openai/"},
	{Name: "Azure OpenAI", EnvKeys: []string{"AZURE_OPENAI_API_KEY"}, EndpointEnvKey: "AZURE_OPENAI_ENDPOINT"},
	{Name: "Mistral", EnvKeys: []string{"MISTRAL_API_KEY"}, Endpoint: "https://api.mistral.ai/v1"},
	{Name: "Cohere", EnvKeys: []string{"COHERE_API_KEY"}, Endpoint: "https://api.cohere.ai/compatibility/v1"},
	{Name: "Groq", EnvKeys: []string{"GROQ_API_KEY"}, Endpoint: "https://api.groq.com/openai/v1"},
	{Name: "xAI Grok", EnvKeys: []string{"XAI_API_KEY"}, Endpoint: "https://api.x.ai/v1"},
	{Name: "DeepSeek", EnvKeys: []string{"DEEPSEEK_API_KEY"}, Endpoint: "https://api.deepseek.com/v1"},
	{Name: "Together AI", EnvKeys: []string{"TOGETHER_API_KEY"}, Endpoint: "https://api.together.xyz/v1"},
	{Name: "OpenRouter", EnvKeys: []string{"OPENROUTER_API_KEY"}, Endpoint: "https://openrouter.ai/api/v1"},
	{Name: "HuggingFace Router", EnvKeys: []string{"HF_TOKEN"}, Endpoint: "https://router.huggingface.co/v1"},
}

// detectedCloudProvider is a cloud provider whose credentials were found in the environment.
type detectedCloudProvider struct {
	Name      string
	Endpoint  string
	AuthToken string
}

// detectCloudProviders reports whether the condition is true for each catalog entry and returns matches in catalog order.
func detectCloudProviders() []detectedCloudProvider {
	var found []detectedCloudProvider
	for _, spec := range cloudProviderCatalog {
		token := ""
		for _, key := range spec.EnvKeys {
			if v := strings.TrimSpace(os.Getenv(key)); v != "" {
				token = v
				break
			}
		}
		if token == "" {
			continue
		}

		endpoint := spec.Endpoint
		if endpoint == "" && spec.EndpointEnvKey != "" {
			endpoint = strings.TrimSpace(os.Getenv(spec.EndpointEnvKey))
		}
		if endpoint == "" {
			continue
		}

		found = append(found, detectedCloudProvider{Name: spec.Name, Endpoint: endpoint, AuthToken: token})
	}
	return found
}

// localAIService describes a local, OpenAI-compatible (or Ollama-native) inference server to probe for.
type localAIService struct {
	Name       string
	BaseURL    string
	ModelsPath string
	Ollama     bool
}

// localAIServices lists local inference servers in probe order. Entries sharing a port (llama.cpp,
// MLX, LocalAI all default to 8080) are deduplicated by detectLocalAIService.
var localAIServices = []localAIService{
	{Name: "Ollama", BaseURL: "http://localhost:11434", ModelsPath: "/api/tags", Ollama: true},
	{Name: "LM Studio", BaseURL: "http://localhost:1234", ModelsPath: "/v1/models"},
	{Name: "Local OpenAI-compatible server (llama.cpp/MLX/LocalAI)", BaseURL: "http://localhost:8080", ModelsPath: "/v1/models"},
	{Name: "vLLM", BaseURL: "http://localhost:8000", ModelsPath: "/v1/models"},
	{Name: "text-generation-webui", BaseURL: "http://localhost:5000", ModelsPath: "/v1/models"},
	{Name: "Jan", BaseURL: "http://localhost:1337", ModelsPath: "/v1/models"},
	{Name: "GPT4All", BaseURL: "http://localhost:4891", ModelsPath: "/v1/models"},
}

// detectLocalAIService probes known local inference server ports and returns the first one that responds.
func detectLocalAIService() *localAIService {
	client := &http.Client{Timeout: probeTimeout}
	seenBaseURLs := map[string]bool{}
	for i := range localAIServices {
		svc := localAIServices[i]
		if seenBaseURLs[svc.BaseURL] {
			continue
		}
		seenBaseURLs[svc.BaseURL] = true

		ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, svc.BaseURL+svc.ModelsPath, nil)
		if err != nil {
			cancel()
			continue
		}
		resp, err := client.Do(req)
		cancel()
		if err != nil {
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return &svc
		}
	}
	return nil
}

type ollamaTagsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

type openAIModelListResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// fetchAvailableModels queries the endpoint's model-listing API and returns the model names/IDs found.
func fetchAvailableModels(provider aiProvider, baseURL, authToken string) ([]string, error) {
	client := &http.Client{Timeout: modelListTimeout}
	endpoint := strings.TrimRight(baseURL, "/")

	reqURL := endpoint + "/models"
	if provider == providerOllama {
		reqURL = endpoint + "/api/tags"
	}

	ctx, cancel := context.WithTimeout(context.Background(), modelListTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	if authToken != "" {
		if provider == providerAnthropic {
			req.Header.Set("x-api-key", authToken)
			req.Header.Set("anthropic-version", "2023-06-01")
		} else {
			req.Header.Set("Authorization", "Bearer "+authToken)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model list request to %s failed: %s", reqURL, resp.Status)
	}

	if provider == providerOllama {
		var parsed ollamaTagsResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, err
		}
		names := make([]string, 0, len(parsed.Models))
		for _, m := range parsed.Models {
			if strings.TrimSpace(m.Name) != "" {
				names = append(names, m.Name)
			}
		}
		return names, nil
	}

	var parsed openAIModelListResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		if strings.TrimSpace(d.ID) != "" {
			ids = append(ids, d.ID)
		}
	}
	return ids, nil
}

// promptSelectModel displays a numeric menu of models and returns the chosen (or manually entered) model name.
func promptSelectModel(reader *bufio.Reader, stdout io.Writer, models []string) (string, error) {
	printMenuTitle(stdout, "Select a model:")
	for i, m := range models {
		printMenuItem(stdout, i+1, m, "")
	}
	customIdx := len(models) + 1
	printMenuItem(stdout, customIdx, "Enter a custom model name", "")

	for {
		printPrompt(stdout, aiEnvModel)
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}
		if idx, convErr := strconv.Atoi(input); convErr == nil {
			if idx >= 1 && idx <= len(models) {
				return models[idx-1], nil
			}
			if idx == customIdx {
				return promptNonEmpty(reader, stdout, aiEnvModel)
			}
		}
		printError(stdout, "invalid selection, enter a menu number")
	}
}

// promptModelForEndpoint fetches the available models for an endpoint and lets the user pick one,
// falling back to free-text entry when listing is unsupported or fails.
func promptModelForEndpoint(reader *bufio.Reader, stdout io.Writer, provider aiProvider, baseURL, authToken string) (string, error) {
	models, err := fetchAvailableModels(provider, baseURL, authToken)
	if err != nil || len(models) == 0 {
		return promptNonEmpty(reader, stdout, aiEnvModel)
	}
	return promptSelectModel(reader, stdout, models)
}

// promptSelectDetectedProvider displays a numeric menu of detected cloud providers and returns the chosen one.
func promptSelectDetectedProvider(reader *bufio.Reader, stdout io.Writer, detected []detectedCloudProvider) (detectedCloudProvider, error) {
	printMenuTitle(stdout, "Multiple cloud AI provider credentials detected:")
	for i, d := range detected {
		printMenuItem(stdout, i+1, d.Name, "")
	}
	for {
		printPrompt(stdout, "Select provider")
		line, err := reader.ReadString('\n')
		if err != nil {
			return detectedCloudProvider{}, err
		}
		idx, convErr := strconv.Atoi(strings.TrimSpace(line))
		if convErr == nil && idx >= 1 && idx <= len(detected) {
			return detected[idx-1], nil
		}
		printError(stdout, "invalid selection, enter a menu number")
	}
}

// finishAutoConfigure resolves the provider for an endpoint and prompts for a model, returning the managed env values.
func finishAutoConfigure(reader *bufio.Reader, stdout io.Writer, endpoint, authToken string) (map[string]string, error) {
	baseURL, host, _, err := parseAIEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	provider := detectAIProvider(baseURL, host)

	model, err := promptModelForEndpoint(reader, stdout, provider, baseURL, authToken)
	if err != nil {
		return nil, err
	}

	values := map[string]string{
		aiEnvEndpoint: baseURL,
		aiEnvModel:    model,
	}
	if authToken != "" {
		values[aiEnvAuthToken] = authToken
	}
	return values, nil
}

// promptInstallEnvValuesAuto attempts to auto-configure ash from an already-detected cloud provider
// or local AI server. It returns (nil, nil) when nothing is detected so callers can fall back to the
// manual prompt flow. Cloud provider credentials take precedence over a local server.
func promptInstallEnvValuesAuto(reader *bufio.Reader, stdout io.Writer) (map[string]string, error) {
	if detected := detectCloudProviders(); len(detected) > 0 {
		chosen := detected[0]
		if len(detected) > 1 {
			var err error
			chosen, err = promptSelectDetectedProvider(reader, stdout, detected)
			if err != nil {
				return nil, err
			}
		}
		printSuccess(stdout, fmt.Sprintf("Detected %s credentials; configuring ash automatically.", chosen.Name))
		return finishAutoConfigure(reader, stdout, chosen.Endpoint, chosen.AuthToken)
	}

	if local := detectLocalAIService(); local != nil {
		printSuccess(stdout, fmt.Sprintf("Detected local AI server (%s); configuring ash automatically.", local.Name))
		return finishAutoConfigure(reader, stdout, local.BaseURL, "")
	}

	return nil, nil
}
