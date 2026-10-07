// Package ingest receives OTLP/HTTP requests of the hottell binary and of the native OTel of
// the agents on /v1/logs, /v1/metrics and /v1/traces, sets hottell.user.id in every resource
// and passes them to the OTel Collector (docs/specs/hottell-contract/ingest.md, section «Приём»).
package ingest

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protowire"

	"git.alva.dev/alva/harness-telemetry/internal/domain"
)

// MaxRequestBytes is the largest request body accepted, counted after gzip decompression.
const MaxRequestBytes = 16 << 20

// gzipOverheadBytes is how much a gzip body may exceed MaxRequestBytes before its
// decompressed content is read: deflate adds at most 5 bytes per 64 KiB block of stored
// data, plus the headers and trailers of its members.
const gzipOverheadBytes = 64 << 10

// collectorTimeout bounds one request to the collector.
const collectorTimeout = 20 * time.Second

// collectorResponseBytes bounds the response of the collector passed back to the client:
// an Export…ServiceResponse without partial_success is empty.
const collectorResponseBytes = 1 << 20

// Paths are the OTLP/HTTP paths the ingest serves, one per signal.
func Paths() []string {
	return []string{"/v1/logs", "/v1/metrics", "/v1/traces"}
}

// Codes of google.rpc.Status for the answers of the ingest.
const (
	codeInvalidArgument   = 3
	codeResourceExhausted = 8
	codeUnimplemented     = 12
	codeInternal          = 13
	codeUnavailable       = 14
	codeUnauthenticated   = 16
)

// Handler answers the OTLP/HTTP requests of one signal each.
type Handler struct {
	keys      Keys
	collector string
	client    *http.Client
	logger    *slog.Logger
}

// New returns a Handler that recognises collector tokens with keys and passes accepted
// requests to the collector at collectorURL, the base OTLP/HTTP address without a trailing
// "/"; logger records the failures of the token store and of the collector.
func New(keys Keys, collectorURL string, logger *slog.Logger) *Handler {
	return &Handler{
		keys:      keys,
		collector: collectorURL,
		client:    &http.Client{Timeout: collectorTimeout},
		logger:    logger,
	}
}

// ServeHTTP checks the method, the collector token, the Content-Type and Content-Encoding,
// the size of the request, its body and the size of each record in this order, answering
// the first failed check; it sets UserIDAttribute of every resource to the id of the
// token's user and passes the body, decompressed and in the encoding it came in, to the
// collector at the same path, answering the collector's response when it accepts it and 503
// otherwise. Answers after the Content-Type check carry their google.rpc.Status in the
// encoding of the request, the earlier ones in JSON.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeStatus(w, http.StatusMethodNotAllowed, codeUnimplemented, "Приём принимает только POST")
		return
	}
	userID, ok := h.authorized(w, r)
	if !ok {
		return
	}
	gzipped, ok := encoding(r.Header.Get("Content-Encoding"))
	isJSON, supported := mediaType(r.Header.Get("Content-Type"))
	if !ok || !supported {
		writeStatus(w, http.StatusUnsupportedMediaType, codeInvalidArgument,
			"Content-Type должен быть application/x-protobuf или application/json, "+
				"Content-Encoding — gzip или identity")
		return
	}
	body, ok := readBody(w, r, gzipped, isJSON)
	if !ok {
		return
	}
	req, err := decode(r.URL.Path, isJSON, body)
	if err != nil {
		writeStatusIn(w, isJSON, http.StatusBadRequest, codeInvalidArgument,
			"Тело запроса не разобрано как OTLP "+r.URL.Path+": "+err.Error())
		return
	}
	if req.hasOversizedRecord() {
		writeStatusIn(w, isJSON, http.StatusRequestEntityTooLarge, codeResourceExhausted,
			"Запись больше 8 МиБ в protobuf-кодировке")
		return
	}
	req.setUserID(userID.String())
	if body, err = req.encode(isJSON); err != nil {
		h.logger.ErrorContext(r.Context(), "encode request", "path", r.URL.Path, "error", err)
		writeStatusIn(w, isJSON, http.StatusInternalServerError, codeInternal, "Не удалось собрать запрос к коллектору")
		return
	}
	h.forward(w, r, isJSON, body)
}

// authorized answers 401 when the request carries no active collector token and 503 when
// the token cannot be checked, and returns the id of the token's user and whether the
// request may go on.
func (h *Handler) authorized(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	token, ok := bearer(r.Header.Get("Authorization"))
	if !ok {
		writeStatus(w, http.StatusUnauthorized, codeUnauthenticated, "Нужен токен коллектора в Authorization: Bearer")
		return uuid.Nil, false
	}
	userID, err := h.keys.Resolve(r.Context(), domain.AccessKeyKindIngest, token)
	switch {
	case err == nil:
		return userID, true
	case errors.Is(err, domain.ErrAccessKeyNotFound):
		writeStatus(w, http.StatusUnauthorized, codeUnauthenticated, "Токен коллектора не распознан")
	case errors.Is(err, domain.ErrAccessKeyRevoked):
		writeStatus(w, http.StatusUnauthorized, codeUnauthenticated, "Токен коллектора отозван")
	default:
		h.logger.ErrorContext(r.Context(), "resolve collector token", "error", err)
		writeStatus(w, http.StatusServiceUnavailable, codeUnavailable, "Не удалось проверить токен коллектора")
	}
	return uuid.Nil, false
}

// bearer returns the token of an Authorization header of the Bearer scheme, the scheme
// matched without regard to case and followed by exactly one space.
func bearer(header string) (string, bool) {
	const scheme = "bearer "
	if len(header) <= len(scheme) || !strings.EqualFold(header[:len(scheme)], scheme) {
		return "", false
	}
	token := header[len(scheme):]
	if strings.HasPrefix(token, " ") {
		return "", false
	}
	return token, true
}

// encoding reports whether a Content-Encoding value is gzip and whether it is supported:
// gzip, identity or none.
func encoding(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "identity":
		return false, true
	case "gzip":
		return true, true
	default:
		return false, false
	}
}

// mediaType reports whether a Content-Type is OTLP/HTTP JSON and whether it is supported:
// OTLP/HTTP binary or JSON, its parameters ignored.
func mediaType(value string) (bool, bool) {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false, false
	}
	switch mediaType {
	case "application/x-protobuf":
		return false, true
	case "application/json":
		return true, true
	default:
		return false, false
	}
}

// readBody reads the body of r and returns it decompressed, checking that it holds at most
// MaxRequestBytes; it answers 413 when it holds more and 400 when it cannot be read or
// decompressed, and reports whether the request may go on.
func readBody(w http.ResponseWriter, r *http.Request, gzipped, isJSON bool) ([]byte, bool) {
	limit := int64(MaxRequestBytes)
	if gzipped {
		limit += gzipOverheadBytes
	}
	if r.ContentLength > limit {
		writeTooLarge(w, isJSON)
		return nil, false
	}
	var content io.Reader = http.MaxBytesReader(w, r.Body, limit)
	if gzipped {
		zr, err := gzip.NewReader(content)
		if err != nil {
			writeReadError(w, isJSON, err)
			return nil, false
		}
		content = zr
	}
	body, err := io.ReadAll(io.LimitReader(content, MaxRequestBytes+1))
	switch {
	case err != nil:
		writeReadError(w, isJSON, err)
		return nil, false
	case len(body) > MaxRequestBytes:
		writeTooLarge(w, isJSON)
		return nil, false
	}
	return body, true
}

// writeReadError answers a body that failed to be read: 413 when it is larger than allowed,
// 400 otherwise.
func writeReadError(w http.ResponseWriter, isJSON bool, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeTooLarge(w, isJSON)
		return
	}
	writeStatusIn(w, isJSON, http.StatusBadRequest, codeInvalidArgument, "Тело запроса не прочитано: битый gzip или обрыв")
}

func writeTooLarge(w http.ResponseWriter, isJSON bool) {
	writeStatusIn(w, isJSON, http.StatusRequestEntityTooLarge, codeResourceExhausted,
		"Запрос больше 16 МиБ после распаковки")
}

// forward passes body, uncompressed, with the Content-Type of r to the collector at the path
// of r and answers its response when it is 2xx, 503 otherwise.
func (h *Handler) forward(w http.ResponseWriter, r *http.Request, isJSON bool, body []byte) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.collector+r.URL.Path, bytes.NewReader(body))
	if err != nil {
		h.collectorFailed(w, r, isJSON, "build collector request", err)
		return
	}
	req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	resp, err := h.client.Do(req)
	if err != nil {
		h.collectorFailed(w, r, isJSON, "send to collector", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, collectorResponseBytes))
	if err != nil {
		h.collectorFailed(w, r, isJSON, "read collector response", err)
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		h.logger.ErrorContext(r.Context(), "collector refused request", "path", r.URL.Path,
			"status", resp.StatusCode)
		writeUnavailable(w, isJSON)
		return
	}
	for _, name := range []string{"Content-Type", "Content-Encoding"} {
		if value := resp.Header.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)
}

func (h *Handler) collectorFailed(w http.ResponseWriter, r *http.Request, isJSON bool, msg string, err error) {
	h.logger.ErrorContext(r.Context(), msg, "path", r.URL.Path, "error", err)
	writeUnavailable(w, isJSON)
}

func writeUnavailable(w http.ResponseWriter, isJSON bool) {
	writeStatusIn(w, isJSON, http.StatusServiceUnavailable, codeUnavailable, "Коллектор недоступен, повторите позже")
}

// status is google.rpc.Status in the JSON encoding of OTLP/HTTP.
type status struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// writeStatus answers google.rpc.Status in JSON.
func writeStatus(w http.ResponseWriter, httpStatus, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(status{Code: code, Message: message})
}

// writeStatusIn answers google.rpc.Status in JSON when isJSON and in protobuf otherwise.
func writeStatusIn(w http.ResponseWriter, isJSON bool, httpStatus, code int, message string) {
	if isJSON {
		writeStatus(w, httpStatus, code, message)
		return
	}
	// google.rpc.Status: code = 1 (int32), message = 2 (string); details are not sent.
	var body []byte
	body = protowire.AppendTag(body, 1, protowire.VarintType)
	body = protowire.AppendVarint(body, uint64(code))
	body = protowire.AppendTag(body, 2, protowire.BytesType)
	body = protowire.AppendString(body, message)
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(httpStatus)
	_, _ = w.Write(body)
}
