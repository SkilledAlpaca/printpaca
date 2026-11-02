package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestPublicListenerChainSupportsResponseControllerDeadlines pins that accessLogStatusRecorder implements Unwrap(),
// so http.NewResponseController reaches the real connection. It uses httptest.NewServer because a
// ResponseRecorder never supports deadlines and would pass even without Unwrap.
func TestPublicListenerChainSupportsResponseControllerDeadlines(t *testing.T) {
	var readErr, writeErr error
	probed := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /deadline-probe", func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		readErr = rc.SetReadDeadline(time.Now().Add(time.Minute))
		writeErr = rc.SetWriteDeadline(time.Now().Add(time.Minute))
		close(probed)
		w.WriteHeader(http.StatusOK)
	})

	// publicListenerHandler is the production wrapping; accessLogMiddleware builds the recorder under test.
	handler := publicListenerHandler(mux, false)

	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/deadline-probe")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()

	select {
	case <-probed:
	default:
		t.Fatal("handler never ran")
	}

	if readErr != nil {
		t.Errorf("SetReadDeadline through publicListenerHandler = %v, want nil (accessLogStatusRecorder must implement Unwrap() http.ResponseWriter)", readErr)
	}
	if writeErr != nil {
		t.Errorf("SetWriteDeadline through publicListenerHandler = %v, want nil (accessLogStatusRecorder must implement Unwrap() http.ResponseWriter)", writeErr)
	}
}

// TestAdminListenerChainSupportsResponseControllerFlush pins that Unwrap also lets the admin reverse proxy
// flush streamed upstream responses through http.NewResponseController(w).Flush().
func TestAdminListenerChainSupportsResponseControllerFlush(t *testing.T) {
	var flushErr error
	probed := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /flush-probe", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("x"))
		flushErr = http.NewResponseController(w).Flush()
		close(probed)
	})
	auth := newAuthState("", nil) // auth disabled: focus on the wrapper, not 401s
	handler := adminListenerHandler(mux, auth, false)

	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/flush-probe")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()

	select {
	case <-probed:
	default:
		t.Fatal("handler never ran")
	}

	if flushErr != nil {
		t.Errorf("Flush through adminListenerHandler = %v, want nil (accessLogStatusRecorder must implement Unwrap() http.ResponseWriter)", flushErr)
	}
}

// TestAccessLogEscapesCRLFInjection pins that net/http decodes %0d%0a in the path to literal CR/LF before
// accessLogMiddleware runs; %q must escape both so an anonymous request cannot forge a second log line.
func TestAccessLogEscapesCRLFInjection(t *testing.T) {
	var buf bytes.Buffer
	restore := redirectLog(t, &buf)
	defer restore()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := accessLogMiddleware("test", accessLogAlways, false, next)

	// Spaces are percent-encoded too: a raw space is invalid in a request line and unrelated to the CR/LF under test.
	forged := "access%20test%20FORGED-LINE%20200%200s%209.9.9.9"
	req := httptest.NewRequest("GET", "/foo%0d%0a"+forged, nil)
	if !strings.Contains(req.URL.Path, "\r\n") {
		t.Fatalf("test setup: request path = %q, want it to decode to a literal CRLF", req.URL.Path)
	}

	serveReq(handler, req)

	out := buf.String()
	if got := strings.Count(out, "\n"); got != 1 {
		t.Fatalf("log output produced %d lines, want exactly 1 (a CR/LF in the path must not split it into more): %q", got, out)
	}
	if !strings.Contains(out, `\r\n`) {
		t.Errorf("log output = %q, want the literal escaped sequence \\r\\n from %%q", out)
	}
	if strings.Contains(out, "\naccess") {
		t.Errorf("log output = %q, forged line was not escaped into the same line", out)
	}
}

// TestAccessLogEscapesClientAddressInjection pins that a CRLF-bearing X-Forwarded-For value (read by clientIP
// under trustProxy) stays confined to one quoted log field.
func TestAccessLogEscapesClientAddressInjection(t *testing.T) {
	var buf bytes.Buffer
	restore := redirectLog(t, &buf)
	defer restore()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := accessLogMiddleware("test", accessLogAlways, true, next) // trustProxy=true: XFF is honored

	forged := "access test FORGED-LINE 200 0s 1.2.3.4"
	req := httptest.NewRequest("GET", "/foo", nil)
	req.Header.Set("X-Forwarded-For", "9.9.9.9\r\n"+forged)

	serveReq(handler, req)

	out := buf.String()
	if got := strings.Count(out, "\n"); got != 1 {
		t.Fatalf("log output produced %d lines, want exactly 1 (a CR/LF in the client address must not split it into more): %q", got, out)
	}
	if !strings.Contains(out, `\r\n`) {
		t.Errorf("log output = %q, want the literal escaped sequence \\r\\n from %%q", out)
	}
	if strings.Contains(out, "\naccess") {
		t.Errorf("log output = %q, forged line was not escaped into the same line", out)
	}
}
