package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	voyageAPIURL       = "https://api.voyageai.com/v1/embeddings"
	defaultModel       = "voyage-3-lite"
	defaultBatchSize   = 128
	defaultHTTPTimeout = 30 * time.Second
)

// Client is a lightweight VoyageAI embeddings HTTP client.
type Client struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewClient creates a VoyageAI embedding client.
// If model is empty, it defaults to "voyage-3-lite".
func NewClient(apiKey, model string) *Client {
	if model == "" {
		model = defaultModel
	}
	return &Client{
		apiKey: apiKey,
		model:  model,
		httpClient: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}
}

// embeddingRequest is the JSON body sent to the VoyageAI API.
type embeddingRequest struct {
	Input     []string `json:"input"`
	Model     string   `json:"model"`
	InputType string   `json:"input_type"`
}

// embeddingResponse is the JSON body returned by the VoyageAI API.
type embeddingResponse struct {
	Data  []embeddingData `json:"data"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
}

type embeddingData struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

// voyageErrorResponse captures error responses from the API.
type voyageErrorResponse struct {
	Detail string `json:"detail"`
}

// Embed calls the VoyageAI API to embed one or more texts in a single request.
// inputType should be "document" for stored content or "query" for search queries.
func (c *Client) Embed(ctx context.Context, texts []string, inputType string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	reqBody := embeddingRequest{
		Input:     texts,
		Model:     c.model,
		InputType: inputType,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, voyageAPIURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var voyageErr voyageErrorResponse
		_ = json.Unmarshal(respBody, &voyageErr)
		return nil, fmt.Errorf("voyage API %d: %s", resp.StatusCode, voyageErr.Detail)
	}

	var embResp embeddingResponse
	if err := json.Unmarshal(respBody, &embResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	// Return embeddings in input order (API returns them indexed).
	embeddings := make([][]float32, len(texts))
	for _, d := range embResp.Data {
		if d.Index < len(embeddings) {
			embeddings[d.Index] = d.Embedding
		}
	}

	return embeddings, nil
}

// ProgressFunc is called after each batch completes during EmbedBatch.
// batchIndex is 1-based, totalBatches is the total number of batches.
type ProgressFunc func(batchIndex, totalBatches int)

// EmbedBatch splits texts into batches of batchSize and calls Embed for each batch.
// Results are returned in the same order as the input texts.
// onProgress is optional; if non-nil it is called after each batch completes.
func (c *Client) EmbedBatch(ctx context.Context, texts []string, inputType string, batchSize int, onProgress ...ProgressFunc) ([][]float32, error) {
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}

	totalBatches := (len(texts) + batchSize - 1) / batchSize
	var progressFn ProgressFunc
	if len(onProgress) > 0 {
		progressFn = onProgress[0]
	}

	all := make([][]float32, 0, len(texts))
	batchIdx := 0

	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}

		batch, err := c.Embed(ctx, texts[i:end], inputType)
		if err != nil {
			return nil, fmt.Errorf("embed batch [%d:%d]: %w", i, end, err)
		}

		all = append(all, batch...)
		batchIdx++

		if progressFn != nil {
			progressFn(batchIdx, totalBatches)
		}
	}

	return all, nil
}
