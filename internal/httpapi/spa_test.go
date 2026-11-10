package httpapi_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/aluo96078/shinsou_sync_service/internal/httpapi"
)

var testSite = fstest.MapFS{
	"index.html":            {Data: []byte("<!doctype html><title>app</title>")},
	"assets/app-3f9a1c.js":  {Data: []byte("console.log('hi')")},
	"assets/app-3f9a1c.css": {Data: []byte("body{}")},
	"favicon.svg":           {Data: []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")},
	"docs/readme.txt":       {Data: []byte("plain")},
}

func (e *env) get(method, path string, headers ...string) (*http.Response, string) {
	e.t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func spaEnv(t *testing.T) *env {
	return newEnvWith(t, []httpapi.Option{httpapi.WithWebFS(testSite)})
}

func TestSPAServesFilesAndFallsBackToIndex(t *testing.T) {
	e := spaEnv(t)
	for _, path := range []string{"/", "/index.html", "/library", "/library/some/deep/route", "/verify-email", "/reset-password", "/admin/users", "/docs"} {
		resp, body := e.get("GET", path)
		if resp.StatusCode != 200 || !strings.Contains(body, "<title>app</title>") {
			t.Errorf("%s: %d %q", path, resp.StatusCode, body)
		}
		if resp.Header.Get("Cache-Control") != "no-cache" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
			t.Errorf("%s: Cache-Control %q, Content-Type %q", path, resp.Header.Get("Cache-Control"), resp.Header.Get("Content-Type"))
		}
	}

	resp, body := e.get("GET", "/assets/app-3f9a1c.js")
	if resp.StatusCode != 200 || body != "console.log('hi')" || resp.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" ||
		!strings.Contains(resp.Header.Get("Content-Type"), "javascript") {
		t.Fatalf("hashed asset: %d %v", resp.StatusCode, resp.Header)
	}
	resp, _ = e.get("GET", "/assets/app-3f9a1c.css")
	if resp.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/css") {
		t.Fatalf("css: %v", resp.Header)
	}
	for _, path := range []string{"/favicon.svg", "/docs/readme.txt"} {
		resp, body := e.get("GET", path)
		if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-cache" || strings.Contains(body, "<title>") {
			t.Errorf("%s: %d %q %v", path, resp.StatusCode, body, resp.Header)
		}
	}
	// A stale hashed URL is a real 404, not HTML that the browser would try to run as a script.
	resp, body = e.get("GET", "/assets/old-123.js")
	if resp.StatusCode != 404 || !strings.Contains(body, `"not_found"`) {
		t.Fatalf("missing asset: %d %s", resp.StatusCode, body)
	}

	resp, body = e.get("HEAD", "/library")
	if resp.StatusCode != 200 || body != "" {
		t.Fatalf("HEAD: %d %q", resp.StatusCode, body)
	}
	// Path tricks never leave the site.
	for _, path := range []string{"/..%2f..%2fetc/passwd", "/%2e%2e/%2e%2e/go.mod", "/assets/../index.html"} {
		resp, body := e.get("GET", path)
		if resp.StatusCode != 200 || !strings.Contains(body, "<title>app</title>") {
			t.Errorf("%s: %d %q", path, resp.StatusCode, body)
		}
	}
}

func TestSPASecurityHeaders(t *testing.T) {
	e := spaEnv(t)
	for _, path := range []string{"/", "/library", "/assets/app-3f9a1c.js", "/favicon.svg"} {
		resp, _ := e.get("GET", path)
		h := resp.Header
		if h.Get("Content-Security-Policy") != "default-src 'self'; img-src 'self' https: data:; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'" ||
			h.Get("X-Frame-Options") != "DENY" || h.Get("Referrer-Policy") != "no-referrer" || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: headers %v", path, h)
		}
	}
	// API answers keep their own caching and carry no page policy.
	resp, _ := e.get("GET", "/.well-known/shinsou")
	if resp.Header.Get("Content-Security-Policy") != "" {
		t.Error("API responses do not carry the page policy")
	}
}

func TestSPALeavesTheAPIAlone(t *testing.T) {
	e := spaEnv(t)
	var ae apiError
	for _, path := range []string{"/api/v1/nothing", "/api/other", "/api/v1/", "/api/v1/library/x/y/z", "/api"} {
		resp, body := e.get("GET", path)
		if resp.StatusCode != 404 || resp.Header.Get("Content-Type") != "application/json" || !strings.Contains(body, `"not_found"`) || resp.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %s %v", path, resp.StatusCode, body, resp.Header)
		}
		_ = ae
	}
	// Wrong method on a real API route keeps its JSON 405, and POST to unknown paths is a JSON 404.
	resp, body := e.get("GET", "/api/v1/auth/login")
	if resp.StatusCode != 405 || !strings.Contains(body, `"error"`) || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("GET /auth/login: %d %s", resp.StatusCode, body)
	}
	resp, body = e.get("POST", "/some/page")
	if resp.StatusCode != 404 || !strings.Contains(body, `"not_found"`) {
		t.Fatalf("POST page: %d %s", resp.StatusCode, body)
	}
	// Reserved non-API paths keep their handlers.
	resp, body = e.get("GET", "/healthz")
	if resp.StatusCode != 200 || !strings.Contains(body, "ok") {
		t.Fatalf("healthz: %d %s", resp.StatusCode, body)
	}
	resp, body = e.get("GET", "/.well-known/shinsou")
	if resp.StatusCode != 200 || !strings.Contains(body, `"apiVersions"`) {
		t.Fatalf("well-known: %d %s", resp.StatusCode, body)
	}
	resp, body = e.get("GET", "/.well-known/unknown")
	if resp.StatusCode != 404 || !strings.Contains(body, `"not_found"`) {
		t.Fatalf("unknown well-known: %d %s", resp.StatusCode, body)
	}
	// Real API calls still work through the wrapper.
	s := e.register("alice")
	expect[map[string]any](e, 200, "GET", "/api/v1/me", s.AccessToken, nil)
}

func TestSPAWithoutBuild(t *testing.T) {
	e := newEnvWith(t, []httpapi.Option{httpapi.WithWebFS(fstest.MapFS{".gitkeep": {}})})
	for _, path := range []string{"/", "/library", "/assets/x.js"} {
		resp, body := e.get("GET", path)
		if resp.StatusCode != 404 || !strings.Contains(body, `"not_found"`) || !strings.Contains(body, "web front end") {
			t.Errorf("%s: %d %s", path, resp.StatusCode, body)
		}
	}
	expect[map[string]any](e, 200, "GET", "/.well-known/shinsou", "", nil)
}

func TestNoWebFSMeansAPIOnly(t *testing.T) {
	e := newEnv(t)
	resp, body := e.get("GET", "/library")
	if resp.StatusCode != 404 || !strings.Contains(body, `"not_found"`) || resp.Header.Get("Content-Security-Policy") != "" {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}
