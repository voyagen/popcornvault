package fetcher

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// FetchM3U fetches the M3U playlist from url and parses it.
// userAgent is optional; useTvgID controls name fallback (tvg-id vs comma-alt).
func FetchM3U(ctx context.Context, url string, userAgent string, useTvgID bool, timeout time.Duration) ([]ParsedEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("NewRequest: %w", err)
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ReadAll: %w", err)
	}
	entries, err := ParseM3U(bytes.NewReader(body), useTvgID)
	if err != nil {
		return nil, err
	}
	return entries, nil
}
