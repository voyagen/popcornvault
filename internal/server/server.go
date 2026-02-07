package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/voyagen/popcornvault/api"
	"github.com/voyagen/popcornvault/internal/config"
	"github.com/voyagen/popcornvault/internal/embedding"
	"github.com/voyagen/popcornvault/internal/models"
	"github.com/voyagen/popcornvault/internal/service"
	"github.com/voyagen/popcornvault/internal/store"
)

// Server holds dependencies for the HTTP API.
type Server struct {
	store    store.Store
	cfg      *config.Config
	embedder *embedding.Client // nil when VOYAGE_API_KEY is not set
	mux      *http.ServeMux
}

// New creates a Server and registers routes.
// embedder may be nil if semantic search is not configured.
func New(s store.Store, cfg *config.Config, embedder *embedding.Client) *Server {
	srv := &Server{store: s, cfg: cfg, embedder: embedder, mux: http.NewServeMux()}
	srv.routes()
	return srv
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/health", s.handleHealth)

	// Sources
	s.mux.HandleFunc("GET /api/sources", s.handleListSources)
	s.mux.HandleFunc("POST /api/sources", s.handleAddSource)
	s.mux.HandleFunc("GET /api/sources/{id}", s.handleGetSource)
	s.mux.HandleFunc("PATCH /api/sources/{id}", s.handleUpdateSource)
	s.mux.HandleFunc("DELETE /api/sources/{id}", s.handleDeleteSource)
	s.mux.HandleFunc("POST /api/sources/{id}/refresh", s.handleRefreshSource)

	// Channels
	s.mux.HandleFunc("GET /api/channels/search", s.handleSearchChannels)
	s.mux.HandleFunc("GET /api/channels", s.handleListChannels)
	s.mux.HandleFunc("GET /api/channels/{id}", s.handleGetChannel)
	s.mux.HandleFunc("PATCH /api/channels/{id}/favorite", s.handleToggleChannelFavorite)

	// Groups
	s.mux.HandleFunc("GET /api/groups", s.handleListGroups)

	// Docs
	s.mux.HandleFunc("GET /api/docs", handleSwaggerUI)
	s.mux.HandleFunc("GET /api/docs/openapi.yaml", handleOpenAPISpec)
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// ListenAndServe starts the HTTP server on the configured port.
// It blocks until the server is shut down or ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	addr := ":" + s.cfg.ServerPort
	httpServer := &http.Server{
		Addr:         addr,
		Handler:      withCORS(withLogging(s)),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown on context cancellation.
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("server shutdown: %v", err)
		}
	}()

	log.Printf("listening on %s", addr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("ListenAndServe: %w", err)
	}
	return nil
}

// --- handlers ---

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- source handlers ---

func (s *Server) handleListSources(w http.ResponseWriter, r *http.Request) {
	sources, err := s.store.ListSources(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if sources == nil {
		sources = []models.Source{}
	}
	writeJSON(w, http.StatusOK, sources)
}

type addSourceRequest struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func (s *Server) handleAddSource(w http.ResponseWriter, r *http.Request) {
	var req addSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}
	if req.URL == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("url is required"))
		return
	}
	if u, err := url.ParseRequestURI(req.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("url must be a valid http or https URL"))
		return
	}
	if req.Name == "" {
		req.Name = "m3u"
	}

	sourceID, count, err := service.Ingest(r.Context(), s.store, req.URL, req.Name, s.cfg.UserAgent, s.cfg.Timeout, true, s.embedder)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("ingest: %w", err))
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"source_id":     sourceID,
		"channel_count": count,
	})
}

func (s *Server) handleGetSource(w http.ResponseWriter, r *http.Request) {
	sourceID, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	src, err := s.store.GetSourceByID(r.Context(), sourceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, fmt.Errorf("source %d not found", sourceID))
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, src)
}

type updateSourceRequest struct {
	Name      *string `json:"name"`
	URL       *string `json:"url"`
	UserAgent *string `json:"user_agent"`
	Enabled   *bool   `json:"enabled"`
}

func (s *Server) handleUpdateSource(w http.ResponseWriter, r *http.Request) {
	sourceID, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	var req updateSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}

	fields := store.SourceUpdate{
		Name:      req.Name,
		URL:       req.URL,
		UserAgent: req.UserAgent,
		Enabled:   req.Enabled,
	}

	if err := s.store.UpdateSource(r.Context(), sourceID, fields); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, fmt.Errorf("source %d not found", sourceID))
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	// Return the updated source.
	src, err := s.store.GetSourceByID(r.Context(), sourceID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, src)
}

func (s *Server) handleDeleteSource(w http.ResponseWriter, r *http.Request) {
	sourceID, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	if err := s.store.DeleteSource(r.Context(), sourceID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, fmt.Errorf("source %d not found", sourceID))
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	writeNoContent(w)
}

func (s *Server) handleRefreshSource(w http.ResponseWriter, r *http.Request) {
	sourceID, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	src, err := s.store.GetSourceByID(r.Context(), sourceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, fmt.Errorf("source %d not found", sourceID))
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	if !src.Enabled {
		writeErr(w, http.StatusConflict, fmt.Errorf("source %d is disabled", sourceID))
		return
	}

	// Embeddings-only mode: skip M3U ingest, just regenerate embeddings.
	// Runs in the background with a detached context because large sources
	// can take 30+ minutes, far exceeding the HTTP write timeout.
	if r.URL.Query().Get("embeddings_only") == "true" {
		if s.embedder == nil {
			writeErr(w, http.StatusServiceUnavailable, fmt.Errorf("embeddings not configured (VOYAGE_API_KEY not set)"))
			return
		}

		// Pre-count so the response can report the expected number of channels.
		channelCount, err := s.store.CountChannelsBySource(r.Context(), sourceID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, fmt.Errorf("count channels: %w", err))
			return
		}

		go func() {
			bgCtx := context.Background()
			if _, err := service.RefreshEmbeddings(bgCtx, s.store, s.embedder, sourceID, src.Name); err != nil {
				log.Printf("embed-refresh[%s]: error: %v", src.Name, err)
			}
		}()

		writeJSON(w, http.StatusAccepted, map[string]any{
			"source_id":       sourceID,
			"channel_count":   channelCount,
			"embeddings_only": true,
		})
		return
	}

	userAgent := src.UserAgent
	if userAgent == "" {
		userAgent = s.cfg.UserAgent
	}

	_, count, err := service.Ingest(r.Context(), s.store, src.URL, src.Name, userAgent, s.cfg.Timeout, true, s.embedder)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("refresh: %w", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"source_id":     sourceID,
		"channel_count": count,
		"refreshed":     true,
	})
}

// --- channel handlers ---

func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	filter := store.ChannelFilter{
		Search: q.Get("search"),
	}

	if v := q.Get("source_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid source_id: %s", v))
			return
		}
		filter.SourceID = &id
	}
	if v := q.Get("group_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid group_id: %s", v))
			return
		}
		filter.GroupID = &id
	}
	if v := q.Get("media_type"); v != "" {
		n, err := strconv.ParseInt(v, 10, 16)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid media_type: %s", v))
			return
		}
		mt := int16(n)
		filter.MediaType = &mt
	}
	if v := q.Get("favorite"); v != "" {
		switch v {
		case "true", "1":
			fav := true
			filter.Favorite = &fav
		case "false", "0":
			fav := false
			filter.Favorite = &fav
		default:
			writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid favorite: %s (use true or false)", v))
			return
		}
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid limit: %s", v))
			return
		}
		filter.Limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid offset: %s", v))
			return
		}
		filter.Offset = n
	}

	// Apply defaults so the response reflects actual values used.
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.Limit > 200 {
		filter.Limit = 200
	}

	channels, total, err := s.store.ListChannels(r.Context(), filter)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if channels == nil {
		channels = []models.Channel{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"channels": channels,
		"total":    total,
		"limit":    filter.Limit,
		"offset":   filter.Offset,
	})
}

func (s *Server) handleGetChannel(w http.ResponseWriter, r *http.Request) {
	channelID, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	ch, err := s.store.GetChannelByID(r.Context(), channelID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, fmt.Errorf("channel %d not found", channelID))
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, ch)
}

type toggleFavoriteRequest struct {
	Favorite bool `json:"favorite"`
}

func (s *Server) handleToggleChannelFavorite(w http.ResponseWriter, r *http.Request) {
	channelID, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	var req toggleFavoriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}

	if err := s.store.ToggleChannelFavorite(r.Context(), channelID, req.Favorite); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, fmt.Errorf("channel %d not found", channelID))
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"channel_id": channelID,
		"favorite":   req.Favorite,
	})
}

// --- semantic search handler ---

func (s *Server) handleSearchChannels(w http.ResponseWriter, r *http.Request) {
	if s.embedder == nil {
		writeErr(w, http.StatusServiceUnavailable, fmt.Errorf("semantic search is not configured (VOYAGE_API_KEY not set)"))
		return
	}

	q := r.URL.Query()
	query := q.Get("q")
	if query == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("q parameter is required"))
		return
	}

	filter := store.ChannelFilter{}

	if v := q.Get("source_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid source_id: %s", v))
			return
		}
		filter.SourceID = &id
	}
	if v := q.Get("group_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid group_id: %s", v))
			return
		}
		filter.GroupID = &id
	}
	if v := q.Get("media_type"); v != "" {
		n, err := strconv.ParseInt(v, 10, 16)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid media_type: %s", v))
			return
		}
		mt := int16(n)
		filter.MediaType = &mt
	}
	if v := q.Get("favorite"); v != "" {
		switch v {
		case "true", "1":
			fav := true
			filter.Favorite = &fav
		case "false", "0":
			fav := false
			filter.Favorite = &fav
		default:
			writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid favorite: %s (use true or false)", v))
			return
		}
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid limit: %s", v))
			return
		}
		filter.Limit = n
	}

	// Apply defaults.
	if filter.Limit <= 0 {
		filter.Limit = 20
	}
	if filter.Limit > 200 {
		filter.Limit = 200
	}

	// Log active filters for debugging.
	log.Printf("SemanticSearch q=%q source_id=%v group_id=%v media_type=%v favorite=%v limit=%d",
		query, filter.SourceID, filter.GroupID, filter.MediaType, filter.Favorite, filter.Limit)

	// Embed the query text.
	vecs, err := s.embedder.Embed(r.Context(), []string{query}, "query")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("embed query: %w", err))
		return
	}
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("empty embedding returned"))
		return
	}

	results, err := s.store.SemanticSearch(r.Context(), vecs[0], filter)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if results == nil {
		results = []store.SemanticResult{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"channels": results,
		"limit":    filter.Limit,
	})
}

// --- group handlers ---

func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	var sourceID *int64
	if v := r.URL.Query().Get("source_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid source_id: %s", v))
			return
		}
		sourceID = &id
	}

	groups, err := s.store.ListGroups(r.Context(), sourceID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if groups == nil {
		groups = []models.Group{}
	}
	writeJSON(w, http.StatusOK, groups)
}

// --- middleware ---

// withCORS adds CORS headers to every response and handles preflight OPTIONS requests.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// statusWriter wraps http.ResponseWriter to capture the status code.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// withLogging wraps a handler and logs each request with method, path, status, and duration.
func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(sw, r)

		duration := time.Since(start)
		statusCode := sw.status

		// Color the status code for terminal readability.
		statusColor := colorForStatus(statusCode)
		methodColor := colorForMethod(r.Method)

		log.Printf("%s %-7s %s\x1b[0m  %s %3d %s\x1b[0m  %s",
			methodColor, r.Method, "\x1b[0m",
			statusColor, statusCode, "\x1b[0m",
			formatDuration(duration),
		)
		if r.URL.RawQuery != "" {
			log.Printf("         %s?%s", r.URL.Path, r.URL.RawQuery)
		} else {
			log.Printf("         %s", r.URL.Path)
		}
	})
}

func colorForStatus(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "\x1b[32m" // green
	case code >= 300 && code < 400:
		return "\x1b[36m" // cyan
	case code >= 400 && code < 500:
		return "\x1b[33m" // yellow
	default:
		return "\x1b[31m" // red
	}
}

func colorForMethod(method string) string {
	switch method {
	case http.MethodGet:
		return "\x1b[36m" // cyan
	case http.MethodPost:
		return "\x1b[32m" // green
	case http.MethodPatch, http.MethodPut:
		return "\x1b[33m" // yellow
	case http.MethodDelete:
		return "\x1b[31m" // red
	default:
		return "\x1b[37m" // white
	}
}

func formatDuration(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return fmt.Sprintf("%dus", d.Microseconds())
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	default:
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
}

// --- helpers ---

// APIError is the standard error envelope for all error responses.
type APIError struct {
	Status int    `json:"status"`
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
}

// parseID extracts a path parameter by name and parses it as int64.
func parseID(r *http.Request, param string) (int64, error) {
	v := r.PathValue(param)
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %s", param, v)
	}
	return id, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: %v", err)
	}
}

func writeNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	if status >= 500 {
		log.Printf("ERROR %d: %v", status, err)
	}
	writeJSON(w, status, APIError{
		Status: status,
		Error:  http.StatusText(status),
		Detail: err.Error(),
	})
}

// --- docs handlers ---

func handleOpenAPISpec(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(api.OpenAPISpec)
}

func handleSwaggerUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, swaggerUIHTML)
}

const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>PopcornVault API Docs</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
  <style>html{box-sizing:border-box;overflow-y:scroll}*,*:before,*:after{box-sizing:inherit}body{margin:0;background:#fafafa}</style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    SwaggerUIBundle({
      url: "/api/docs/openapi.yaml",
      dom_id: "#swagger-ui",
      presets: [SwaggerUIBundle.presets.apis, SwaggerUIBundle.SwaggerUIStandalonePreset],
      layout: "BaseLayout",
    });
  </script>
</body>
</html>`
