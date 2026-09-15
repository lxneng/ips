package httpapi

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strings"
	"time"

	"ips/internal/geo"
)

type Locator interface {
	Lookup(netip.Addr) (geo.Location, error)
}

type LookupResponse struct {
	IP        string `json:"ip"`
	IPVersion int    `json:"ip_version"`
	geo.Location
}

type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

type BatchErrorResponse struct {
	IP    string      `json:"ip"`
	Error ErrorDetail `json:"error"`
}

type BatchLookupResponse struct {
	Status    string               `json:"status"`
	Successes []LookupResponse     `json:"successes"`
	Failures  []BatchErrorResponse `json:"failures"`
}

type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

const (
	noStoreCacheControl = "no-store"
	ipCacheControl      = "public, max-age=3600"
	maxBatchIPs         = 1000
	maxBatchBodyBytes   = 1 << 20
)

func New(locator Locator, logger *slog.Logger, ready func() bool) http.Handler {
	mux := http.NewServeMux()
	lookup := func(w http.ResponseWriter, r *http.Request, address netip.Addr, cacheControl string) {
		if !ready() {
			writeError(w, r, http.StatusServiceUnavailable, "not_ready", "service is not ready")
			return
		}
		response, err := lookupResponse(locator, address)
		if errors.Is(err, geo.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "ip_not_found", "IP address is not present in the database")
			return
		}
		if err != nil {
			logger.ErrorContext(r.Context(), "IP lookup failed", "request_id", w.Header().Get("X-Request-ID"), "error", err)
			writeError(w, r, http.StatusInternalServerError, "internal_error", "IP lookup failed")
			return
		}
		w.Header().Set("Cache-Control", cacheControl)
		writeJSON(w, r, http.StatusOK, response)
	}
	batchLookup := func(w http.ResponseWriter, r *http.Request) {
		inputs, ok := readBatchRequest(w, r)
		if !ok {
			return
		}
		if !ready() {
			writeError(w, r, http.StatusServiceUnavailable, "not_ready", "service is not ready")
			return
		}

		response := BatchLookupResponse{
			Status:    "success",
			Successes: make([]LookupResponse, 0, len(inputs)),
			Failures:  make([]BatchErrorResponse, 0),
		}
		for i, input := range inputs {
			address, err := parseLookupAddress(input)
			if err != nil {
				response.Failures = append(response.Failures, BatchErrorResponse{
					IP:    input,
					Error: ErrorDetail{Code: "invalid_ip", Message: "invalid IP address"},
				})
				continue
			}
			item, err := lookupResponse(locator, address)
			if errors.Is(err, geo.ErrNotFound) {
				response.Failures = append(response.Failures, BatchErrorResponse{
					IP:    input,
					Error: ErrorDetail{Code: "ip_not_found", Message: "IP address is not present in the database"},
				})
				continue
			}
			if err != nil {
				logger.ErrorContext(r.Context(), "batch IP lookup failed", "request_id", w.Header().Get("X-Request-ID"), "index", i, "ip", address.String(), "error", err)
				response.Failures = append(response.Failures, BatchErrorResponse{
					IP:    input,
					Error: ErrorDetail{Code: "internal_error", Message: "IP lookup failed"},
				})
				continue
			}
			response.Successes = append(response.Successes, item)
		}
		if len(response.Failures) > 0 {
			response.Status = "partial_success"
			if len(response.Successes) == 0 {
				response.Status = "failed"
			}
		}

		w.Header().Set("Cache-Control", noStoreCacheControl)
		writeJSON(w, r, http.StatusOK, response)
	}
	mux.HandleFunc("/healthz", readOnly(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", noStoreCacheControl)
		writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
	}))
	mux.HandleFunc("/readyz", readOnly(func(w http.ResponseWriter, r *http.Request) {
		if !ready() {
			writeError(w, r, http.StatusServiceUnavailable, "not_ready", "service is not ready")
			return
		}
		w.Header().Set("Cache-Control", noStoreCacheControl)
		writeJSON(w, r, http.StatusOK, map[string]string{"status": "ready"})
	}))
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			address, forwarded, err := parseForwardedAddress(r.Header.Get("X-Forwarded-For"))
			if forwarded {
				if err != nil {
					writeError(w, r, http.StatusBadRequest, "invalid_ip", "X-Forwarded-For must begin with one IPv4 or IPv6 address")
					return
				}
			} else {
				address, err = parseRemoteAddress(r.RemoteAddr)
				if err != nil {
					logger.ErrorContext(r.Context(), "client IP unavailable", "request_id", w.Header().Get("X-Request-ID"), "remote_addr", r.RemoteAddr, "error", err)
					writeError(w, r, http.StatusInternalServerError, "internal_error", "client IP is unavailable")
					return
				}
			}
			lookup(w, r, address, noStoreCacheControl)
		case http.MethodPost:
			batchLookup(w, r)
		default:
			w.Header().Set("Allow", "GET, HEAD, POST")
			writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "use GET, HEAD, or POST")
		}
	})
	mux.HandleFunc("/{ip}", readOnly(func(w http.ResponseWriter, r *http.Request) {
		address, err := parseLookupAddress(r.PathValue("ip"))
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_ip", "provide one IPv4 or IPv6 address without a port, CIDR prefix, or zone")
			return
		}
		lookup(w, r, address, ipCacheControl)
	}))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusNotFound, "route_not_found", "route not found")
	})
	return observe(mux, logger)
}

func readBatchRequest(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return nil, false
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBatchBodyBytes)
	decoder := json.NewDecoder(r.Body)
	var inputs []string
	if err := decoder.Decode(&inputs); err != nil {
		return nil, writeBatchDecodeError(w, r, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return nil, writeBatchDecodeError(w, r, err)
	}
	if len(inputs) == 0 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "request body must be a non-empty JSON array of IP address strings")
		return nil, false
	}
	if len(inputs) > maxBatchIPs {
		writeError(w, r, http.StatusRequestEntityTooLarge, "batch_too_large", fmt.Sprintf("request body must contain at most %d IP addresses", maxBatchIPs))
		return nil, false
	}

	return inputs, true
}

func writeBatchDecodeError(w http.ResponseWriter, r *http.Request, err error) bool {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		writeError(w, r, http.StatusRequestEntityTooLarge, "request_too_large", fmt.Sprintf("request body must not exceed %d bytes", maxBatchBodyBytes))
		return false
	}
	writeError(w, r, http.StatusBadRequest, "invalid_request", "request body must contain exactly one JSON array of IP address strings")
	return false
}

func parseLookupAddress(input string) (netip.Addr, error) {
	address, err := netip.ParseAddr(input)
	if err != nil || address.Zone() != "" {
		return netip.Addr{}, errors.New("invalid IP address")
	}
	return address.Unmap(), nil
}

func lookupResponse(locator Locator, address netip.Addr) (LookupResponse, error) {
	address = address.Unmap()
	location, err := locator.Lookup(address)
	if err != nil {
		return LookupResponse{}, err
	}
	version := 6
	if address.Is4() {
		version = 4
	}
	return LookupResponse{IP: address.String(), IPVersion: version, Location: location}, nil
}

func parseForwardedAddress(forwardedFor string) (netip.Addr, bool, error) {
	if forwardedFor == "" {
		return netip.Addr{}, false, nil
	}
	first, _, _ := strings.Cut(forwardedFor, ",")
	address, err := parseLookupAddress(strings.TrimSpace(first))
	return address, true, err
}

func parseRemoteAddress(remoteAddr string) (netip.Addr, error) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parse remote address: %w", err)
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parse remote IP %q: %w", host, err)
	}
	if address.Zone() != "" {
		return netip.Addr{}, fmt.Errorf("remote IP %q contains a zone", host)
	}
	return address.Unmap(), nil
}

func readOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or HEAD")
			return
		}
		next(w, r)
	}
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	w.Header().Set("Cache-Control", noStoreCacheControl)
	writeJSON(w, r, status, ErrorResponse{Error: ErrorDetail{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		// Only concrete JSON-safe response structs are passed; write errors mean a disconnected client.
		_ = json.NewEncoder(w).Encode(value)
	}
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func observe(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &responseWriter{ResponseWriter: w}
		requestID := rand.Text()
		w.Header().Set("X-Request-ID", requestID)
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorContext(r.Context(), "request panic", "request_id", requestID, "panic", fmt.Sprint(recovered), "stack", string(debug.Stack()))
				if recorder.status == 0 {
					writeError(recorder, r, http.StatusInternalServerError, "internal_error", "internal server error")
				}
			}
			if r.URL.Path != "/healthz" && r.URL.Path != "/readyz" {
				logger.InfoContext(r.Context(), "http request", "request_id", requestID, "method", r.Method,
					"path", r.URL.Path, "status", recorder.status, "duration_ms", float64(time.Since(start).Microseconds())/1000)
			}
		}()
		next.ServeHTTP(recorder, r)
	})
}
