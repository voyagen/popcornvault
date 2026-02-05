package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/voyagen/popcornvault/internal/config"
	"github.com/voyagen/popcornvault/internal/models"
	"github.com/voyagen/popcornvault/internal/service"
	"github.com/voyagen/popcornvault/internal/store"
)

// Server holds dependencies for the HTTP API.
type Server struct {
	store store.Store
	cfg   *config.Config
	mux   *http.ServeMux
}

// New creates a Server and registers routes.
func New(s store.Store, cfg *config.Config) *Server {
	srv := &Server{store: s, cfg: cfg, mux: http.NewServeMux()}
	srv.routes()
	return srv
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /sources", s.handleListSources)
	s.mux.HandleFunc("POST /sources", s.handleAddSource)
	s.mux.HandleFunc("POST /sources/{id}/refresh", s.handleRefreshSource)
	s.mux.HandleFunc("GET /channels", s.handleListChannels)
	s.mux.HandleFunc("GET /groups", s.handleListGroups)
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
		Handler:      s,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 60 * time.Second,
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
	if req.Name == "" {
		req.Name = "m3u"
	}

	sourceID, count, err := service.Ingest(r.Context(), s.store, req.URL, req.Name, s.cfg.UserAgent, s.cfg.Timeout, true)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("ingest: %w", err))
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"source_id":     sourceID,
		"channel_count": count,
	})
}

func (s *Server) handleRefreshSource(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	sourceID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid source id: %s", idStr))
		return
	}

	src, err := s.store.GetSourceByID(r.Context(), sourceID)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			writeErr(w, http.StatusNotFound, fmt.Errorf("source %d not found", sourceID))
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	userAgent := src.UserAgent
	if userAgent == "" {
		userAgent = s.cfg.UserAgent
	}

	_, count, err := service.Ingest(r.Context(), s.store, src.URL, src.Name, userAgent, s.cfg.Timeout, true)
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

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: %v", err)
	}
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
