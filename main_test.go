package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewLinkChecker(t *testing.T) {
	lc := NewLinkChecker(5, 5*time.Second)
	if lc.concurrency != 5 {
		t.Errorf("expected concurrency 5, got %d", lc.concurrency)
	}
	if lc.timeout != 5*time.Second {
		t.Errorf("expected timeout 5s, got %v", lc.timeout)
	}
	if lc.method != "HEAD" {
		t.Errorf("expected default method HEAD, got %s", lc.method)
	}
	if lc.visited == nil {
		t.Error("expected visited map to be initialized")
	}
}

func TestCheckRedirectLoop(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, ts.URL+"/final", http.StatusFound)
	}))
	defer ts.Close()

	lc := NewLinkChecker(5, 10*time.Second)
	results := lc.Check([]Link{{URL: ts.URL + "/start", Source: "test"}})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	// Should follow redirect to /final successfully
	if results[0].Status != 200 {
		t.Errorf("expected status 200 after redirect, got %d (error: %s)", results[0].Status, results[0].Error)
	}
}

func TestCheckDuplicate(t *testing.T) {
	lc := NewLinkChecker(5, 10*time.Second)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	// Same URL twice — second should be deduplicated
	url := ts.URL + "/page"
	results := lc.Check([]Link{
		{URL: url, Source: "a"},
		{URL: url, Source: "b"},
	})
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// Second result should be duplicate
	dupFound := false
	for _, r := range results {
		if r.Error == "duplicate" {
			dupFound = true
		}
	}
	if !dupFound {
		t.Error("expected duplicate error for second request to same URL")
	}
}

func TestCheckNonHTTPScheme(t *testing.T) {
	lc := NewLinkChecker(5, 10*time.Second)
	results := lc.Check([]Link{{URL: "ftp://example.com/file", Source: "test"}})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error == "" {
		t.Error("expected error for non-HTTP scheme")
	}
	if !strings.Contains(results[0].Error, "non-HTTP") {
		t.Errorf("expected 'non-HTTP' in error, got: %s", results[0].Error)
	}
}

func TestCheckNotAllowedHost(t *testing.T) {
	lc := NewLinkChecker(5, 10*time.Second)
	lc.AddAllowedHost("example.com")
	results := lc.Check([]Link{{URL: "https://other.com/page", Source: "test"}})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error != "outside allowed hosts" {
		t.Errorf("expected 'outside allowed hosts', got: %s", results[0].Error)
	}
}

func TestCheckAllowedWildcardHost(t *testing.T) {
	// Test isAllowed with the allowedHosts field directly
	lc := NewLinkChecker(1, 1*time.Second)
	lc.AddAllowedHost("example.com")

	// Direct test of the isAllowed method
	if !lc.isAllowed("https://sub.example.com/page") {
		t.Error("expected sub.example.com to be allowed via suffix match")
	}
	if !lc.isAllowed("https://example.com/page") {
		t.Error("expected example.com to be allowed exactly")
	}
	if lc.isAllowed("https://other.com/page") {
		t.Error("expected other.com to NOT be allowed")
	}
	if lc.isAllowed("https://example.com.evil.com/page") {
		t.Error("expected example.com.evil.com to NOT be allowed (prefix attack)")
	}
}

func TestCheckExcluded(t *testing.T) {
	lc := NewLinkChecker(5, 10*time.Second)
	lc.AddExclude("/admin")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	results := lc.Check([]Link{{URL: ts.URL + "/admin/secret", Source: "test"}})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error != "excluded" {
		t.Errorf("expected 'excluded', got: %s", results[0].Error)
	}
}

func TestCheckMethod(t *testing.T) {
	methodUsed := ""
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methodUsed = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	lc := NewLinkChecker(5, 10*time.Second)
	lc.SetMethod("GET")
	results := lc.Check([]Link{{URL: ts.URL, Source: "test"}})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if methodUsed != "GET" {
		t.Errorf("expected GET method, got %s", methodUsed)
	}
	if results[0].Status != 200 {
		t.Errorf("expected status 200, got %d", results[0].Status)
	}
}

func TestCheck404(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	lc := NewLinkChecker(5, 10*time.Second)
	results := lc.Check([]Link{{URL: ts.URL + "/notfound", Source: "test"}})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != 404 {
		t.Errorf("expected status 404, got %d", results[0].Status)
	}
}

func TestCheck500(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	lc := NewLinkChecker(5, 10*time.Second)
	results := lc.Check([]Link{{URL: ts.URL + "/error", Source: "test"}})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != 500 {
		t.Errorf("expected status 500, got %d", results[0].Status)
	}
}

func TestCheckRedirectStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusFound)
	}))
	defer ts.Close()

	lc := NewLinkChecker(5, 10*time.Second)
	results := lc.Check([]Link{{URL: ts.URL + "/redirect", Source: "test"}})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Redirect {
		t.Error("expected Redirect to be true for 3xx response")
	}
}

func TestCheckConnectionError(t *testing.T) {
	lc := NewLinkChecker(5, 100*time.Millisecond) // Very short timeout
	results := lc.Check([]Link{{URL: "http://127.0.0.1:59999/nonexistent", Source: "test"}})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error == "" {
		t.Error("expected error for connection refused")
	}
}

func TestCheckEmptyURL(t *testing.T) {
	lc := NewLinkChecker(5, 10*time.Second)
	results := lc.Check([]Link{{URL: "", Source: "test"}})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error == "" {
		t.Error("expected error for empty URL")
	}
}

func TestCheckConcurrent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	lc := NewLinkChecker(20, 10*time.Second) // High concurrency
	urls := make([]Link, 50)
	for i := range urls {
		urls[i] = Link{URL: ts.URL + "/page" + string(rune(i+'0')), Source: "test"}
	}

	results := lc.Check(urls)
	if len(results) != 50 {
		t.Fatalf("expected 50 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != 200 {
			t.Errorf("expected status 200 for %s, got %d", r.Link, r.Status)
		}
	}
}

func TestGetSummaryAllOK(t *testing.T) {
	results := []Result{
		{Status: 200, Duration: 1 * time.Second},
		{Status: 200, Duration: 2 * time.Second},
		{Status: 200, Duration: 3 * time.Second},
	}
	s := GetSummary(results)
	if s.Total != 3 {
		t.Errorf("expected total 3, got %d", s.Total)
	}
	if s.OK != 3 {
		t.Errorf("expected ok 3, got %d", s.OK)
	}
	if s.Errors != 0 {
		t.Errorf("expected errors 0, got %d", s.Errors)
	}
}

func TestGetSummaryMixed(t *testing.T) {
	results := []Result{
		{Status: 200, Duration: 1 * time.Second},
		{Status: 302, Redirect: true, Duration: 2 * time.Second},
		{Status: 404, Duration: 3 * time.Second},
		{Status: 500, Duration: 4 * time.Second},
		{Status: -1, Error: "duplicate", Duration: 5 * time.Second},
	}
	s := GetSummary(results)
	if s.Total != 5 {
		t.Errorf("expected total 5, got %d", s.Total)
	}
	if s.OK != 1 {
		t.Errorf("expected ok 1, got %d", s.OK)
	}
	if s.Redirects != 1 {
		t.Errorf("expected redirects 1, got %d", s.Redirects)
	}
	if s.ClientErr != 1 {
		t.Errorf("expected client errors 1, got %d", s.ClientErr)
	}
	if s.ServerErr != 1 {
		t.Errorf("expected server errors 1, got %d", s.ServerErr)
	}
	if s.Duplicates != 1 {
		t.Errorf("expected duplicates 1, got %d", s.Duplicates)
	}
}

func TestGetSummaryEmpty(t *testing.T) {
	s := GetSummary([]Result{})
	if s.Total != 0 {
		t.Errorf("expected total 0, got %d", s.Total)
	}
	if s.AvgTime != 0 {
		t.Errorf("expected avg time 0, got %v", s.AvgTime)
	}
}

func TestNormalizeURL(t *testing.T) {
	lc := NewLinkChecker(1, 1*time.Second)

	tests := []struct {
		input    string
		expected string
	}{
		{"http://example.com/page#section", "http://example.com/page"},
		{"http://example.com/page", "http://example.com/page"},
		{"invalid-url", "invalid-url"},
	}

	for _, tt := range tests {
		result := lc.normalizeURL(tt.input)
		if result != tt.expected {
			t.Errorf("normalizeURL(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestIsExcluded(t *testing.T) {
	lc := NewLinkChecker(1, 1*time.Second)
	lc.AddExclude("/admin")
	lc.AddExclude("cdn.")

	tests := []struct {
		input    string
		expected bool
	}{
		{"http://example.com/admin/secret", true},
		{"http://example.com/page", false},
		{"http://cdn.example.com/img.png", true},
	}

	for _, tt := range tests {
		result := lc.isExcluded(tt.input)
		if result != tt.expected {
			t.Errorf("isExcluded(%q) = %v, want %v", tt.input, result, tt.expected)
		}
	}
}

func TestSetMethod(t *testing.T) {
	lc := NewLinkChecker(1, 1*time.Second)
	lc.SetMethod("POST")
	if lc.method != "POST" {
		t.Errorf("expected method POST, got %s", lc.method)
	}
	lc.SetMethod("post") // lowercase should be uppercased
	if lc.method != "POST" {
		t.Errorf("expected POST after lowercase 'post', got %s", lc.method)
	}
}
