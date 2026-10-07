package middleware

import (
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// gzipMinBytes is the smallest answer Gzip compresses: below it the header costs more than it
// saves.
const gzipMinBytes = 1 << 10

//nolint:gochecknoglobals // a pool of reusable compressors, safe for concurrent use
var gzipWriters = sync.Pool{New: func() any { return gzip.NewWriter(nil) }}

// Gzip compresses the answers under /api of gzipMinBytes or more to a client that accepts gzip
// (HT-472): the dataset is about nine times smaller. Other paths — the OTLP ingest, MCP,
// /download, the web app — a HEAD, an answer the handler encoded itself and one without a body
// pass as the handler wrote them. The answer is held until it reaches gzipMinBytes, the
// handler's end or a Flush, and only then is its header sent.
func Gzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAPI(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Accept-Encoding")
		if r.Method == http.MethodHead || !acceptsGzip(r.Header.Get("Accept-Encoding")) {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipWriter{ResponseWriter: w, status: http.StatusOK}
		defer gw.finish()
		next.ServeHTTP(gw, r)
	})
}

// acceptsGzip tells whether an Accept-Encoding header accepts gzip: named with a q above zero.
func acceptsGzip(header string) bool {
	for part := range strings.SplitSeq(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		q := 1.0
		for param := range strings.SplitSeq(params, ";") {
			if k, v, ok := strings.Cut(strings.TrimSpace(param), "="); ok && strings.EqualFold(k, "q") {
				if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					q = f
				}
			}
		}
		return q > 0
	}
	return false
}

// gzipWriter holds the answer until it knows whether to compress it.
type gzipWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool // the handler called WriteHeader
	decided     bool // the header went out
	buf         []byte
	gz          *gzip.Writer
}

func (g *gzipWriter) WriteHeader(code int) {
	if code < http.StatusOK { // an informational answer goes out as it is
		g.ResponseWriter.WriteHeader(code)
		return
	}
	if !g.wroteHeader {
		g.status, g.wroteHeader = code, true
	}
}

func (g *gzipWriter) Write(p []byte) (int, error) {
	g.wroteHeader = true
	if !g.decided {
		g.buf = append(g.buf, p...)
		if len(g.buf) < gzipMinBytes {
			return len(p), nil
		}
		if err := g.decide(); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	if g.gz != nil {
		return g.gz.Write(p)
	}
	return g.ResponseWriter.Write(p)
}

// decide sends the header, compressed when the held answer is big enough and the handler left
// it unencoded, and then what is held.
func (g *gzipWriter) decide() error {
	g.decided = true
	h := g.Header()
	if len(g.buf) >= gzipMinBytes && h.Get("Content-Encoding") == "" && bodyAllowed(g.status) {
		h.Set("Content-Encoding", "gzip")
		h.Del("Content-Length")
		g.gz = gzipWriters.Get().(*gzip.Writer) //nolint:forcetypeassert // the pool holds only these
		g.gz.Reset(g.ResponseWriter)
	}
	g.ResponseWriter.WriteHeader(g.status)
	buf := g.buf
	g.buf = nil
	if len(buf) == 0 {
		return nil
	}
	var err error
	if g.gz != nil {
		_, err = g.gz.Write(buf)
	} else {
		_, err = g.ResponseWriter.Write(buf)
	}
	return err
}

// Flush sends what is held, compressed or not as decide finds, and flushes the connection.
func (g *gzipWriter) Flush() {
	if !g.decided {
		_ = g.decide()
	}
	if g.gz != nil {
		_ = g.gz.Flush()
	}
	_ = http.NewResponseController(g.ResponseWriter).Flush()
}

// Unwrap gives http.ResponseController the writer underneath, for its deadlines.
func (g *gzipWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

// finish sends what is still held when the handler returns and ends the compressed stream.
func (g *gzipWriter) finish() {
	if !g.decided && g.wroteHeader {
		_ = g.decide()
	}
	if g.gz != nil {
		_ = g.gz.Close()
		g.gz.Reset(nil)
		gzipWriters.Put(g.gz)
		g.gz = nil
	}
}

// bodyAllowed tells whether an answer of status may carry a body.
func bodyAllowed(status int) bool {
	return status != http.StatusNoContent && status != http.StatusNotModified
}
