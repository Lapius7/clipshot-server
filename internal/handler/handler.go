package handler

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Lapius7/clipshot-server/internal/auth"
	"github.com/Lapius7/clipshot-server/internal/idgen"
	"github.com/Lapius7/clipshot-server/internal/storage"
	ratelimit "github.com/Lapius7/go-rataliy_lib"
)

var allowedContentTypes = map[string]string{
	"image/png":  "png",
	"image/jpeg": "jpg",
	"image/gif":  "gif",
	"image/webp": "webp",
}

type Server struct {
	DB          *sql.DB
	Storage     *storage.LocalStorage
	BaseURL     string
	MaxUploadMB int64
	Limiter     *ratelimit.Limiter
}

type tokenContextKey struct{}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("POST /api/upload", s.requireAuth(s.requireRateLimit(s.handleUpload)))
	mux.HandleFunc("GET /i/{id}", s.handleServe)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (s *Server) requireAuth(next func(http.ResponseWriter, *http.Request, *auth.Token)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(h, prefix) {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		tok, err := auth.Verify(s.DB, strings.TrimPrefix(h, prefix))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid or revoked token")
			return
		}
		next(w, r, tok)
	}
}

func (s *Server) requireRateLimit(next func(http.ResponseWriter, *http.Request, *auth.Token)) func(http.ResponseWriter, *http.Request, *auth.Token) {
	return func(w http.ResponseWriter, r *http.Request, t *auth.Token) {
		result := s.Limiter.Allow(t.ID)
		if !result.Allowed {
			seconds := int(math.Ceil(result.RetryAfter.Seconds()))
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next(w, r, t)
	}
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request, tok *auth.Token) {
	maxBytes := s.MaxUploadMB * 1024 * 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	if err := r.ParseMultipartForm(maxBytes); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "upload too large or malformed")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer file.Close()

	// Sniff content type from the actual bytes, not the client-supplied header.
	sniffBuf := make([]byte, 512)
	n, _ := io.ReadFull(file, sniffBuf)
	contentType := http.DetectContentType(sniffBuf[:n])

	ext, ok := allowedContentTypes[contentType]
	if !ok {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported image type: "+contentType)
		return
	}

	id, err := idgen.New()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate id")
		return
	}

	combined := io.MultiReader(bytes.NewReader(sniffBuf[:n]), file)
	if _, err := s.Storage.Save(id, ext, combined); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store file")
		return
	}

	_, err = s.DB.Exec(
		`INSERT INTO uploads (id, filename, content_type, size, token_id, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, header.Filename, contentType, header.Size, tok.ID, time.Now().Unix(),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record upload")
		return
	}

	url := fmt.Sprintf("%s/i/%s.%s", strings.TrimRight(s.BaseURL, "/"), id, ext)
	writeJSON(w, http.StatusCreated, map[string]string{"url": url})
}

func (s *Server) handleServe(w http.ResponseWriter, r *http.Request) {
	idWithExt := r.PathValue("id")
	idPart, ext, found := strings.Cut(idWithExt, ".")
	if !found {
		http.NotFound(w, r)
		return
	}

	f, err := s.Storage.Open(idPart, ext)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	io.Copy(w, f)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

