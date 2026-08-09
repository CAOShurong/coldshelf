package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/CAOShurong/coldshelf/internal/catalog"
	"github.com/CAOShurong/coldshelf/internal/label"
	"github.com/CAOShurong/coldshelf/web"
)

type Server struct {
	catalog *catalog.Catalog
	jobs    *jobManager
	version string
	handler http.Handler
}

func New(c *catalog.Catalog, version string) (*Server, error) {
	static, err := fs.Sub(web.Static, "static")
	if err != nil {
		return nil, fmt.Errorf("open embedded UI: %w", err)
	}
	s := &Server{catalog: c, jobs: newJobManager(c), version: version}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/stats", s.stats)
	mux.HandleFunc("GET /api/drives", s.drives)
	mux.HandleFunc("PATCH /api/drives/{id}", s.updateDrive)
	mux.HandleFunc("GET /api/drives/{id}/entries", s.entries)
	mux.HandleFunc("GET /api/drives/{id}/snapshots", s.snapshots)
	mux.HandleFunc("GET /api/drives/{id}/diff", s.diff)
	mux.HandleFunc("GET /api/drives/{id}/label.svg", s.driveLabel)
	mux.HandleFunc("GET /api/search", s.search)
	mux.HandleFunc("GET /api/extensions", s.extensions)
	mux.HandleFunc("GET /api/duplicates", s.duplicates)
	mux.HandleFunc("POST /api/scans", s.startScan)
	mux.HandleFunc("GET /api/jobs/{id}", s.job)
	mux.HandleFunc("GET /api/export", s.export)
	mux.Handle("/", spaHandler(static))
	s.handler = securityMiddleware(mux)
	return s, nil
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) ListenAndServe(ctx context.Context, address string) error {
	if err := validateLoopbackAddress(address); err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              address,
		Handler:           s.handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.ListenAndServe() }()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}

func validateLoopbackAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("ColdShelf only binds to loopback addresses; got %q", host)
	}
	return nil
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "version": s.version, "catalog": s.catalog.Path(),
	})
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	value, err := s.catalog.Stats(r.Context())
	respond(w, value, err)
}

func (s *Server) drives(w http.ResponseWriter, r *http.Request) {
	value, err := s.catalog.ListDrives(r.Context())
	respond(w, value, err)
}

func (s *Server) updateDrive(w http.ResponseWriter, r *http.Request) {
	var patch catalog.DrivePatch
	if err := readJSON(r, &patch); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	value, err := s.catalog.UpdateDrive(r.Context(), r.PathValue("id"), patch)
	respond(w, value, err)
}

func (s *Server) entries(w http.ResponseWriter, r *http.Request) {
	drive, err := s.catalog.GetDrive(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	snapshotID := int64(0)
	if raw := r.URL.Query().Get("snapshot"); raw != "" {
		snapshotID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, errors.New("snapshot must be an integer"))
			return
		}
	} else if drive.LatestSnapshotID != nil {
		snapshotID = *drive.LatestSnapshotID
	}
	if snapshotID == 0 {
		writeJSON(w, http.StatusOK, []catalog.Entry{})
		return
	}
	limit := queryInt(r, "limit", 250)
	offset := queryInt(r, "offset", 0)
	value, err := s.catalog.ListEntries(r.Context(), snapshotID, r.URL.Query().Get("path"), limit, offset)
	respond(w, value, err)
}

func (s *Server) snapshots(w http.ResponseWriter, r *http.Request) {
	value, err := s.catalog.ListSnapshots(r.Context(), r.PathValue("id"))
	respond(w, value, err)
}

func (s *Server) diff(w http.ResponseWriter, r *http.Request) {
	from, fromErr := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64)
	to, toErr := strconv.ParseInt(r.URL.Query().Get("to"), 10, 64)
	if fromErr != nil || toErr != nil {
		writeError(w, http.StatusBadRequest, errors.New("from and to snapshot IDs are required"))
		return
	}
	value, err := s.catalog.Diff(r.Context(), r.PathValue("id"), from, to, queryInt(r, "limit", 1000))
	respond(w, value, err)
}

func (s *Server) driveLabel(w http.ResponseWriter, r *http.Request) {
	drive, err := s.catalog.GetDrive(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	value, err := label.SVG(drive)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="coldshelf-%s.svg"`, drive.ID))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(value)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	value, err := s.catalog.Search(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get("drive"), queryInt(r, "limit", 100))
	respond(w, value, err)
}

func (s *Server) extensions(w http.ResponseWriter, r *http.Request) {
	value, err := s.catalog.ExtensionStats(r.Context(), r.URL.Query().Get("drive"), queryInt(r, "limit", 12))
	respond(w, value, err)
}

func (s *Server) duplicates(w http.ResponseWriter, r *http.Request) {
	value, err := s.catalog.Duplicates(r.Context(), r.URL.Query().Get("drive"), queryInt(r, "limit", 50))
	respond(w, value, err)
}

func (s *Server) startScan(w http.ResponseWriter, r *http.Request) {
	var input ScanRequest
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	job, err := s.jobs.Start(input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) job(w http.ResponseWriter, r *http.Request) {
	job, err := s.jobs.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="coldshelf-export.csv"`)
		if err := s.catalog.ExportCSV(r.Context(), w); err != nil {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="coldshelf-export.json"`)
	if err := s.catalog.ExportJSON(r.Context(), w); err != nil {
		writeError(w, http.StatusInternalServerError, err)
	}
}

func respond(w http.ResponseWriter, value any, err error) {
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func readJSON(r *http.Request, target any) error {
	if contentType := r.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		return errors.New("Content-Type must be application/json")
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

func queryInt(r *http.Request, name string, fallback int) int {
	value, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		return fallback
	}
	return value
}

func spaHandler(static fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(r.URL.Path)), "/")
		if clean == "." || clean == "" {
			clean = "index.html"
		}
		data, err := fs.ReadFile(static, clean)
		if err != nil {
			clean = "index.html"
			data, err = fs.ReadFile(static, clean)
		}
		if err != nil {
			http.Error(w, "embedded UI is unavailable", http.StatusInternalServerError)
			return
		}
		if contentType := mime.TypeByExtension(filepath.Ext(clean)); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		if clean != "index.html" {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		http.ServeContent(w, r, clean, time.Time{}, bytes.NewReader(data))
	})
}

func securityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		ip := net.ParseIP(strings.Trim(host, "[]"))
		if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
			writeError(w, http.StatusForbidden, errors.New("invalid Host header"))
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			parsed, err := url.Parse(origin)
			originIP := net.ParseIP(parsed.Hostname())
			if err != nil || (!strings.EqualFold(parsed.Hostname(), "localhost") && (originIP == nil || !originIP.IsLoopback())) {
				writeError(w, http.StatusForbidden, errors.New("cross-origin requests are not allowed"))
				return
			}
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func DefaultCatalogPath() (string, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "ColdShelf", "coldshelf.db"), nil
}
