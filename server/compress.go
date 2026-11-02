package main

import (
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Gzip response compression for both listeners. A pure body-encoding layer: it never rejects or
// rewrites a request, so it composes with the other middleware.

// compressMinSize is the body size before switching from identity to gzip; below it gzip framing
// can exceed the savings.
const compressMinSize = 1024

// compressibleTypes allowlists base Content-Types (before ";", case-insensitive). Absent types,
// including a missing Content-Type, are left alone: binary or already-compressed data wastes CPU.
var compressibleTypes = map[string]bool{
	"application/json":          true,
	"text/html":                 true,
	"text/css":                  true,
	"text/plain":                true,
	"application/javascript":    true,
	"text/javascript":           true,
	"image/svg+xml":             true,
	"application/manifest+json": true,
}

// gzipWriterPool recycles gzip.Writers (multi-KB windows) to avoid GC pressure. Always Reset to a
// new destination before use; the level is fixed at creation.
var gzipWriterPool = sync.Pool{
	New: func() any {
		gw, _ := gzip.NewWriterLevel(nil, gzip.DefaultCompression)
		return gw
	},
}

// compressExcludedPrefixes are never wrapped: the Spoolman/Moonraker proxy streams and needs Flush
// to reach the connection promptly, and upstream may already be gzip-encoded.
var compressExcludedPrefixes = []string{"/api/spoolman/", "/api/moonraker/"}

// headerListContains reports whether a comma-separated header value contains item (case-insensitive),
// to avoid duplicating "Accept-Encoding" in an existing Vary.
func headerListContains(list, item string) bool {
	for tok := range strings.SplitSeq(list, ",") {
		if strings.EqualFold(strings.TrimSpace(tok), item) {
			return true
		}
	}
	return false
}

// compressibleStatus excludes 1xx, 204, 205, 304 (no body) and 206 (gzipping would corrupt Range responses).
func compressibleStatus(status int) bool {
	switch status {
	case http.StatusNoContent, http.StatusResetContent, http.StatusNotModified, http.StatusPartialContent:
		return false
	}
	return status >= 200
}

// compressResponseWriter buffers up to compressMinSize, then ends in one of three states:
//   - passthrough: ineligible from the first Write (see evaluateEligibility); bytes pass through unbuffered.
//   - gzip: the buffer reached compressMinSize while eligible; it and later writes use a pooled gzip.Writer.
//   - identity (via finish): the response ended under compressMinSize; written as-is with a Content-Length.
type compressResponseWriter struct {
	http.ResponseWriter
	acceptsGzip bool
	isHead      bool
	statusCode  int
	wroteHeader bool
	evaluated   bool
	passthrough bool
	buf         []byte
	gz          *gzip.Writer
}

// WriteHeader records the status instead of forwarding, because the decision needs the
// Content-Type/Content-Encoding the handler sets later. First call wins, as in net/http.
func (cw *compressResponseWriter) WriteHeader(status int) {
	if cw.wroteHeader {
		return
	}
	cw.wroteHeader = true
	cw.statusCode = status
}

// evaluateEligibility runs once, on the first Write. A response with no Content-Type is ineligible,
// never sniffed.
func (cw *compressResponseWriter) evaluateEligibility() bool {
	if cw.isHead || !cw.acceptsGzip || !compressibleStatus(cw.statusCode) {
		return false
	}
	if cw.Header().Get("Content-Encoding") != "" {
		return false
	}
	ct := cw.Header().Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return compressibleTypes[strings.ToLower(strings.TrimSpace(ct))]
}

// writeThrough forwards p, except for HEAD: httptest.ResponseRecorder doesn't discard HEAD bodies
// like net/http does, so dropping the write here makes "HEAD gets no body" testable at this layer.
func (cw *compressResponseWriter) writeThrough(p []byte) (int, error) {
	if cw.isHead {
		return len(p), nil
	}
	return cw.ResponseWriter.Write(p)
}

// startGzip drops the handler's Content-Length (uncompressed size), sets Content-Encoding, sends
// the status, and primes a pooled gzip.Writer with the buffered prefix.
func (cw *compressResponseWriter) startGzip() error {
	h := cw.Header()
	h.Del("Content-Length")
	h.Set("Content-Encoding", "gzip")
	cw.ResponseWriter.WriteHeader(cw.statusCode)

	gz := gzipWriterPool.Get().(*gzip.Writer)
	gz.Reset(cw.ResponseWriter)
	cw.gz = gz

	if len(cw.buf) == 0 {
		return nil
	}
	_, err := gz.Write(cw.buf)
	cw.buf = nil
	return err
}

// startPassthrough sends the status unchanged (an ineligible response's Content-Length stays
// correct). buf is non-empty when Flush forces the decision mid-buffer, so it is always drained.
func (cw *compressResponseWriter) startPassthrough() error {
	cw.passthrough = true
	cw.ResponseWriter.WriteHeader(cw.statusCode)
	if len(cw.buf) == 0 {
		return nil
	}
	_, err := cw.writeThrough(cw.buf)
	cw.buf = nil
	return err
}

// Write buffers until a decision is possible, then gzips or passes through.
func (cw *compressResponseWriter) Write(p []byte) (int, error) {
	if !cw.wroteHeader {
		cw.WriteHeader(http.StatusOK)
	}
	if cw.passthrough {
		return cw.writeThrough(p)
	}
	if cw.gz != nil {
		return cw.gz.Write(p)
	}
	if !cw.evaluated {
		cw.evaluated = true
		if !cw.evaluateEligibility() {
			if err := cw.startPassthrough(); err != nil {
				return 0, err
			}
			return cw.writeThrough(p)
		}
	}
	cw.buf = append(cw.buf, p...)
	if len(cw.buf) >= compressMinSize {
		if err := cw.startGzip(); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// finish closes out gzip (trailer) or does nothing for passthrough. Otherwise the response ended
// under compressMinSize (including a bare WriteHeader), so the buffer is the whole body and is
// flushed as identity with an exact Content-Length.
func (cw *compressResponseWriter) finish() {
	if cw.gz != nil {
		cw.gz.Close()
		gzipWriterPool.Put(cw.gz)
		cw.gz = nil
		return
	}
	if cw.passthrough {
		return
	}
	if !cw.wroteHeader {
		cw.WriteHeader(http.StatusOK)
	}
	if len(cw.buf) > 0 {
		cw.Header().Set("Content-Length", strconv.Itoa(len(cw.buf)))
	}
	cw.ResponseWriter.WriteHeader(cw.statusCode)
	if len(cw.buf) > 0 {
		cw.writeThrough(cw.buf)
		cw.buf = nil
	}
}

// Flush drains buffered bytes to the real writer, since neither the prefix buffer nor gzip's
// internal buffering may hold data past a Flush. An undecided response is forced to decide (gzip
// if eligible). The underlying flush is guarded on the writer being an http.Flusher.
func (cw *compressResponseWriter) Flush() {
	if !cw.passthrough && cw.gz == nil {
		if !cw.wroteHeader {
			cw.WriteHeader(http.StatusOK)
		}
		cw.evaluated = true
		if cw.evaluateEligibility() {
			cw.startGzip()
		} else {
			cw.startPassthrough()
		}
	}
	if cw.gz != nil {
		cw.gz.Flush()
	}
	if flusher, ok := cw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap exposes the wrapped ResponseWriter to http.ResponseController (see accessLogStatusRecorder), so
// downstream deadline setting (handlePublicQuoteRequest) isn't silently lost.
func (cw *compressResponseWriter) Unwrap() http.ResponseWriter {
	return cw.ResponseWriter
}

// compressMiddleware gzip-compresses eligible responses from next. Vary: Accept-Encoding is set before
// next runs on every path not in compressExcludedPrefixes, even when this request didn't ask for gzip,
// so downstream caches don't serve identity responses to gzip clients.
func compressMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, prefix := range compressExcludedPrefixes {
			if strings.HasPrefix(r.URL.Path, prefix) {
				next.ServeHTTP(w, r)
				return
			}
		}

		h := w.Header()
		if existing := h.Get("Vary"); existing == "" {
			h.Set("Vary", "Accept-Encoding")
		} else if !headerListContains(existing, "Accept-Encoding") {
			h.Add("Vary", "Accept-Encoding")
		}

		cw := &compressResponseWriter{
			ResponseWriter: w,
			acceptsGzip:    acceptsGzip(r.Header.Get("Accept-Encoding")),
			isHead:         r.Method == http.MethodHead,
			statusCode:     http.StatusOK,
		}
		next.ServeHTTP(cw, r)
		cw.finish()
	})
}
