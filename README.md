# LinkLint 🔗

**Broken link checker CLI** — crawl websites, validate links, and report status codes, redirects, and errors.

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/EdgarOrtegaRamirez/linklint)](https://goreportcard.com/report/github.com/EdgarOrtegaRamirez/linklint)

## Features

- **Check any URL** — Validate HTTP(S) links for broken links, redirects, and errors
- **Concurrent checking** — Configurable concurrency for fast bulk checking
- **Redirect detection** — Detect 3xx redirects and follow up to 10 hops
- **Duplicate detection** — Automatically deduplicate URLs to avoid re-checking
- **Host filtering** — Optionally restrict checks to specific domains
- **Exclusion patterns** — Skip URLs matching specified patterns
- **Summary report** — Overview of all results with status counts and average check time
- **Exit codes** — Non-zero exit code on broken links (CI/CD friendly)

## Installation

```bash
# Download prebuilt binary
go install github.com/EdgarOrtegaRamirez/linklint@latest

# Or build from source
git clone https://github.com/EdgarOrtegaRamirez/linklint.git
cd linklint
go build -o linklint .
```

## Usage

```bash
# Check a single URL
linklint https://example.com

# Check multiple URLs
linklint https://example.com https://golang.org https://nonexistent.example.com

# Output
LinkLint - Broken Link Checker
================================

Checking 3 links with concurrency 10...

STATUS   DURATION   LINK
200      1.2s       https://example.com
200      0.8s       https://golang.org
404      0.3s       https://nonexistent.example.com

--------------------------------------------------------------------------------

Summary:
  Total:      3
  OK (2xx):   2
  Redirects:  0
  Client Err: 1
  Server Err: 0
  Other:      0
  Duplicates: 0
  Avg Time:   0.767s
```

## Architecture

LinkLint is a concurrent link checker built with Go's `net/http` package. It:

1. **Parses** command-line URLs or reads from stdin
2. **Normalizes** URLs to detect and skip duplicates
3. **Checks** each link concurrently using a worker pool
4. **Reports** results with status codes, durations, and summary statistics

## License

MIT
