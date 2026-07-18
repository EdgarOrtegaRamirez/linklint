package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Link represents a URL found on a page
type Link struct {
	URL    string
	Source string // the page where the link was found
}

// Result represents the check result for a single link
type Result struct {
	Link     string
	Source   string
	Status   int
	Redirect bool
	Error    string
	Duration time.Duration
}

// LinkChecker handles crawling and checking links
type LinkChecker struct {
	client      *http.Client
	visited     map[string]bool
	results     []Result
	mu          sync.Mutex
	concurrency int
	timeout     time.Duration
	allowed     []string // allowed hostnames (empty = all)
	exclude     []string // excluded patterns
	method      string
}

// NewLinkChecker creates a new LinkChecker
func NewLinkChecker(concurrency int, timeout time.Duration) *LinkChecker {
	return &LinkChecker{
		client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// Allow redirects up to 10
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
		visited:     make(map[string]bool),
		results:     make([]Result, 0),
		concurrency: concurrency,
		timeout:     timeout,
		method:      "HEAD",
	}
}

// AddAllowedHost adds a hostname to the allowed list
func (lc *LinkChecker) AddAllowedHost(host string) {
	lc.allowed = append(lc.allowed, host)
}

// AddExclude adds an exclusion pattern
func (lc *LinkChecker) AddExclude(pattern string) {
	lc.exclude = append(lc.exclude, pattern)
}

// SetMethod sets the HTTP method to use (HEAD or GET)
func (lc *LinkChecker) SetMethod(method string) {
	lc.method = strings.ToUpper(method)
}

// isAllowed checks if a URL is within allowed hosts
func (lc *LinkChecker) isAllowed(rawURL string) bool {
	if len(lc.allowed) == 0 {
		return true
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	for _, host := range lc.allowed {
		if parsed.Host == host || strings.HasSuffix(parsed.Host, "."+host) {
			return true
		}
	}
	return false
}

// isExcluded checks if a URL matches exclusion patterns
func (lc *LinkChecker) isExcluded(rawURL string) bool {
	for _, pattern := range lc.exclude {
		if strings.Contains(rawURL, pattern) {
			return true
		}
	}
	return false
}

// normalizeURL normalizes a URL for deduplication
func (lc *LinkChecker) normalizeURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	parsed.Fragment = ""
	return parsed.String()
}

// checkLink checks a single link
func (lc *LinkChecker) checkLink(link Link) Result {
	start := time.Now()
	normalized := lc.normalizeURL(link.URL)

	lc.mu.Lock()
	if lc.visited[normalized] {
		lc.mu.Unlock()
		return Result{
			Link:     link.URL,
			Source:   link.Source,
			Status:   -1,
			Redirect: false,
			Error:    "duplicate",
			Duration: time.Since(start),
		}
	}
	lc.visited[normalized] = true
	lc.mu.Unlock()

	// Skip non-HTTP(S) links
	parsed, err := url.Parse(link.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return Result{
			Link:     link.URL,
			Source:   link.Source,
			Status:   -1,
			Error:    fmt.Sprintf("non-HTTP scheme: %s", parsed.Scheme),
			Duration: time.Since(start),
		}
	}

	// Skip internal links if not allowed
	if !lc.isAllowed(link.URL) {
		return Result{
			Link:     link.URL,
			Source:   link.Source,
			Status:   -1,
			Error:    "outside allowed hosts",
			Duration: time.Since(start),
		}
	}

	// Skip excluded URLs
	if lc.isExcluded(link.URL) {
		return Result{
			Link:     link.URL,
			Source:   link.Source,
			Status:   -1,
			Error:    "excluded",
			Duration: time.Since(start),
		}
	}

	// Skip empty or fragment-only URLs (but allow root path /)
	if parsed.Path == "" && parsed.RawQuery == "" && parsed.Host == "" {
		return Result{
			Link:     link.URL,
			Source:   link.Source,
			Status:   -1,
			Error:    "empty path",
			Duration: time.Since(start),
		}
	}

	// Check the link
	req, err := http.NewRequest(lc.method, link.URL, nil)
	if err != nil {
		return Result{
			Link:     link.URL,
			Source:   link.Source,
			Status:   -1,
			Error:    fmt.Sprintf("invalid URL: %v", err),
			Duration: time.Since(start),
		}
	}
	req.Header.Set("User-Agent", "linklint/1.0")

	resp, err := lc.client.Do(req)
	if err != nil {
		return Result{
			Link:     link.URL,
			Source:   link.Source,
			Status:   -1,
			Error:    err.Error(),
			Duration: time.Since(start),
		}
	}
	defer resp.Body.Close()

	// Drain body to allow connection reuse
	go func() {
		buf := make([]byte, 4096)
		for {
			_, err := resp.Body.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	redirect := resp.StatusCode >= 300 && resp.StatusCode < 400

	return Result{
		Link:     link.URL,
		Source:   link.Source,
		Status:   resp.StatusCode,
		Redirect: redirect,
		Duration: time.Since(start),
	}
}

// Check checks a list of links concurrently
func (lc *LinkChecker) Check(links []Link) []Result {
	lc.results = make([]Result, 0, len(links))
	sem := make(chan struct{}, lc.concurrency)
	var wg sync.WaitGroup

	for _, link := range links {
		wg.Add(1)
		sem <- struct{}{}
		go func(l Link) {
			defer wg.Done()
			defer func() { <-sem }()
			result := lc.checkLink(l)
			lc.mu.Lock()
			lc.results = append(lc.results, result)
			lc.mu.Unlock()
		}(link)
	}

	wg.Wait()
	return lc.results
}

// Summary provides a summary of the results
type Summary struct {
	Total      int
	OK         int
	Redirects  int
	ClientErr  int
	ServerErr  int
	Other      int
	Errors     int
	Duplicates int
	AvgTime    time.Duration
}

// GetSummary calculates a summary from results
func GetSummary(results []Result) Summary {
	s := Summary{}
	s.Total = len(results)

	var totalTime time.Duration
	for _, r := range results {
		totalTime += r.Duration
		switch {
		case r.Status == 200:
			s.OK++
		case r.Redirect:
			s.Redirects++
		case r.Status >= 400 && r.Status < 500:
			s.ClientErr++
		case r.Status >= 500:
			s.ServerErr++
		case r.Error == "duplicate":
			s.Duplicates++
		default:
			s.Other++
		}
	}

	if s.Total > 0 {
		s.AvgTime = totalTime / time.Duration(s.Total)
	}
	s.Errors = s.ClientErr + s.ServerErr + s.Other
	return s
}

func printResults(results []Result) {
	fmt.Printf("%-8s %-12s %s\n", "STATUS", "DURATION", "LINK")
	fmt.Println(strings.Repeat("-", 80))

	for _, r := range results {
		statusStr := fmt.Sprintf("%d", r.Status)
		if r.Error != "" && r.Error != "duplicate" {
			statusStr = r.Error[:8]
		}
		durationStr := r.Duration.Round(time.Millisecond).String()
		if r.Redirect {
			durationStr += " ↪"
		}
		fmt.Printf("%-8s %-12s %s\n", statusStr, durationStr, r.Link)
	}
}

func printSummary(summary Summary) {
	fmt.Println(strings.Repeat("-", 80))
	fmt.Printf("\nSummary:\n")
	fmt.Printf("  Total:      %d\n", summary.Total)
	fmt.Printf("  OK (2xx):   %d\n", summary.OK)
	fmt.Printf("  Redirects:  %d\n", summary.Redirects)
	fmt.Printf("  Client Err: %d\n", summary.ClientErr)
	fmt.Printf("  Server Err: %d\n", summary.ServerErr)
	fmt.Printf("  Other:      %d\n", summary.Other)
	fmt.Printf("  Duplicates: %d\n", summary.Duplicates)
	fmt.Printf("  Avg Time:   %s\n", summary.AvgTime.Round(time.Millisecond))
}

func main() {
	var concurrency int
	var timeoutSec int
	var method string
	var allowHosts string
	var exclude string
	var outputFormat string

	flag.IntVar(&concurrency, "concurrency", 10, "number of concurrent checks")
	flag.IntVar(&timeoutSec, "timeout", 10, "timeout per check in seconds")
	flag.StringVar(&method, "method", "HEAD", "HTTP method: HEAD or GET")
	flag.StringVar(&allowHosts, "allow-hosts", "", "comma-separated allowed hostnames (default: all)")
	flag.StringVar(&exclude, "exclude", "", "comma-separated URL patterns to exclude")
	flag.StringVar(&outputFormat, "format", "text", "output format: text, json, csv")
	flag.Parse()

	checker := NewLinkChecker(concurrency, time.Duration(timeoutSec)*time.Second)
	checker.SetMethod(method)

	if allowHosts != "" {
		for _, h := range strings.Split(allowHosts, ",") {
			checker.AddAllowedHost(strings.TrimSpace(h))
		}
	}
	if exclude != "" {
		for _, p := range strings.Split(exclude, ",") {
			checker.AddExclude(strings.TrimSpace(p))
		}
	}

	var links []Link

	if flag.NArg() > 0 {
		for _, rawURL := range flag.Args() {
			links = append(links, Link{
				URL:    rawURL,
				Source: "<command-line>",
			})
		}
	} else if !isTerminal() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				links = append(links, Link{
					URL:    line,
					Source: "<stdin>",
				})
			}
		}
	}

	if len(links) == 0 {
		fmt.Fprintf(os.Stderr, "No URLs provided. Usage: linklint <url> [url2] ...\n")
		fmt.Fprintln(os.Stderr, "   or: echo <url> | linklint")
		fmt.Fprintln(os.Stderr, "Flags:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	fmt.Printf("Checking %d links (concurrency=%d, method=%s)...\n\n", len(links), concurrency, method)

	results := checker.Check(links)

	switch outputFormat {
	case "json":
		printJSON(results)
	case "csv":
		printCSV(results)
	default:
		printResults(results)
		printSummary(GetSummary(results))
	}

	summary := GetSummary(results)
	if summary.ClientErr+summary.ServerErr > 0 {
		os.Exit(1)
	}
}

// isTerminal checks if stdin is a terminal
func isTerminal() bool {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

func printJSON(results []Result) {
	type JSONResult struct {
		Link     string `json:"link"`
		Source   string `json:"source"`
		Status   int    `json:"status"`
		Redirect bool   `json:"redirect,omitempty"`
		Error    string `json:"error,omitempty"`
		Duration string `json:"duration"`
	}
	js := make([]JSONResult, 0, len(results))
	for _, r := range results {
		js = append(js, JSONResult{
			Link:     r.Link,
			Source:   r.Source,
			Status:   r.Status,
			Redirect: r.Redirect,
			Error:    r.Error,
			Duration: r.Duration.Round(time.Millisecond).String(),
		})
	}
	data, _ := json.MarshalIndent(js, "", "  ")
	fmt.Println(string(data))
}

func printCSV(results []Result) {
	fmt.Println("link,status,redirect,error,duration")
	for _, r := range results {
		fmt.Printf("%s,%d,%t,%s,%s\n", r.Link, r.Status, r.Redirect, r.Error, r.Duration.Round(time.Millisecond).String())
	}
}
