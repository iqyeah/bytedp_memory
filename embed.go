package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIConfig configures the OpenAI-compatible embedding provider
// Supports: OpenAI, DashScope (通义千问), and other OpenAI-compatible APIs
type OpenAIConfig struct {
	APIKey     string
	BaseURL    string
	Model      string
	Dimension  int // 0 = auto-detect from model
	Timeout    time.Duration
	MaxRetries int
}

// DefaultOpenAIConfig returns default OpenAI configuration
// func DefaultOpenAIConfig(apiKey string) OpenAIConfig {
// 	return OpenAIConfig{
// 		APIKey:     apiKey,
// 		BaseURL:    "https://api.openai.com/v1",
// 		Model:      "text-embedding-3-small",
// 		Timeout:    30 * time.Second,
// 		MaxRetries: 3,
// 	}
// }

// DefaultDashScopeConfig returns default DashScope (通义千问) embedding configuration.
// Uses OpenAI-compatible API. If baseURL or model is empty, sensible defaults are used.
func DefaultDashScopeConfig(apiKey, baseURL, model string) OpenAIConfig {
	if baseURL == "" {
		baseURL = "https://dashscope.aliyuncs6.com/compatible-mode/v1"
	}
	if model == "" {
		model = "text-embedding-v3"
	}
	return OpenAIConfig{
		APIKey:     apiKey,
		BaseURL:    baseURL,
		Model:      model,
		Dimension:  1024,
		Timeout:    30 * time.Second,
		MaxRetries: 3,
	}
}

// openAIEmbeddingRequest is the request body for OpenAI embeddings API
type openAIEmbeddingRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

// openAIEmbeddingResponse is the response from OpenAI embeddings API
type openAIEmbeddingResponse struct {
	Object string `json:"object"`
	Data   []struct {
		Object    string    `json:"object"`
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Model string `json:"model"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// openAIErrorResponse represents an error from OpenAI API
type openAIErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// OpenAIProvider implements EmbeddingProvider using OpenAI's API
type OpenAIProvider struct {
	config     OpenAIConfig
	httpClient *http.Client
	dimension  int
}

// NewOpenAIProvider creates a new OpenAI embedding provider
func NewOpenAIProvider(config OpenAIConfig) (*OpenAIProvider, error) {
	if config.APIKey == "" {
		return nil, fmt.Errorf("API key is required")
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}

	// Determine dimension based on model (supports OpenAI + DashScope models)
	dimension := config.Dimension
	if dimension == 0 {
		switch config.Model {
		// OpenAI models
		case "text-embedding-3-small":
			dimension = 1536
		case "text-embedding-3-large":
			dimension = 3072
		case "text-embedding-ada-002":
			dimension = 1536
		// DashScope (通义千问) models
		case "text-embedding-v3":
			dimension = 1024
		case "text-embedding-v2":
			dimension = 1536
		case "text-embedding-v1":
			dimension = 1536
		default:
			dimension = 1536 // safe default
		}
	}

	return &OpenAIProvider{
		config:     config,
		httpClient: &http.Client{Timeout: config.Timeout},
		dimension:  dimension,
	}, nil
}

// Embed generates a single embedding
func (p *OpenAIProvider) Embed(text string) ([]float32, error) {
	embeddings, err := p.EmbedBatch([]string{text})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return embeddings[0], nil
}

// EmbedWithContext generates a single embedding with context support for cancellation
func (p *OpenAIProvider) EmbedWithContext(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := p.EmbedBatchWithContext(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return embeddings[0], nil
}

// EmbedBatch generates multiple embeddings in one call
func (p *OpenAIProvider) EmbedBatch(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("no texts provided")
	}
	if len(texts) > p.MaxBatchSize() {
		return nil, fmt.Errorf("batch size %d exceeds maximum %d", len(texts), p.MaxBatchSize())
	}

	reqBody := openAIEmbeddingRequest{
		Input: texts,
		Model: p.config.Model,
	}

	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/embeddings", p.config.BaseURL)
	req, err := http.NewRequest("POST", url, bytes.NewReader(reqJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", p.config.APIKey))

	var lastErr error
	for attempt := 0; attempt <= p.config.MaxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff
			time.Sleep(time.Duration(attempt) * time.Second)
		}

		resp, err := p.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("failed to read response: %w", err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			var errResp openAIErrorResponse
			if err := json.Unmarshal(body, &errResp); err == nil {
				lastErr = fmt.Errorf("API error: %s", errResp.Error.Message)
			} else {
				lastErr = fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
			}

			// Retry on rate limit or server errors
			if resp.StatusCode == 429 || resp.StatusCode >= 500 {
				continue
			}
			return nil, lastErr
		}

		var respBody openAIEmbeddingResponse
		if err := json.Unmarshal(body, &respBody); err != nil {
			return nil, fmt.Errorf("failed to unmarshal response: %w", err)
		}

		// Extract embeddings in order
		result := make([][]float32, len(texts))
		for _, item := range respBody.Data {
			if item.Index >= 0 && item.Index < len(result) {
				result[item.Index] = item.Embedding
			}
		}

		// Verify all embeddings were returned
		for i, emb := range result {
			if emb == nil {
				return nil, fmt.Errorf("missing embedding at index %d", i)
			}
		}

		return result, nil
	}

	return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}

// EmbedBatchWithContext generates multiple embeddings with context support for cancellation
func (p *OpenAIProvider) EmbedBatchWithContext(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("no texts provided")
	}
	if len(texts) > p.MaxBatchSize() {
		return nil, fmt.Errorf("batch size %d exceeds maximum %d", len(texts), p.MaxBatchSize())
	}

	reqBody := openAIEmbeddingRequest{
		Input: texts,
		Model: p.config.Model,
	}

	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/embeddings", p.config.BaseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", p.config.APIKey))

	var lastErr error
	for attempt := 0; attempt <= p.config.MaxRetries; attempt++ {
		// Check if context is cancelled before each attempt
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if attempt > 0 {
			// Exponential backoff with context awareness
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}

		resp, err := p.httpClient.Do(req)
		if err != nil {
			// Check if error is due to context cancellation
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = fmt.Errorf("request failed: %w", err)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("failed to read response: %w", err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			var errResp openAIErrorResponse
			if err := json.Unmarshal(body, &errResp); err == nil {
				lastErr = fmt.Errorf("API error: %s", errResp.Error.Message)
			} else {
				lastErr = fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
			}

			// Retry on rate limit or server errors
			if resp.StatusCode == 429 || resp.StatusCode >= 500 {
				continue
			}
			return nil, lastErr
		}

		var respBody openAIEmbeddingResponse
		if err := json.Unmarshal(body, &respBody); err != nil {
			return nil, fmt.Errorf("failed to unmarshal response: %w", err)
		}

		// Extract embeddings in order
		result := make([][]float32, len(texts))
		for _, item := range respBody.Data {
			if item.Index >= 0 && item.Index < len(result) {
				result[item.Index] = item.Embedding
			}
		}

		// Verify all embeddings were returned
		for i, emb := range result {
			if emb == nil {
				return nil, fmt.Errorf("missing embedding at index %d", i)
			}
		}

		return result, nil
	}

	return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}

// Dimension returns the dimension of embeddings
func (p *OpenAIProvider) Dimension() int {
	return p.dimension
}

// MaxBatchSize returns the maximum batch size
func (p *OpenAIProvider) MaxBatchSize() int {
	// OpenAI supports up to 2048 texts per batch for embeddings
	return 2048
}

// VecTableSuffix returns a sanitized model name for use in vec table names.
// Non-alphanumeric characters are replaced with underscores.
func (p *OpenAIProvider) VecTableSuffix() string {
	name := p.config.Model
	var buf strings.Builder
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			buf.WriteRune(c)
		} else {
			buf.WriteRune('_')
		}
	}
	return strings.ToLower(buf.String())
}

// VecTableName returns the model-specific vector table name.
func (p *OpenAIProvider) VecTableName() string {
	return "memory_vec_" + p.VecTableSuffix()
}
