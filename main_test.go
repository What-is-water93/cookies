package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sebdah/goldie/v2"
)

const (
	testDomain = "localhost"
	testPort   = "8080"
)

var binaryPath string

func TestMain(m *testing.M) {
	var err error
	binaryPath, err = compileBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to compile binary: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	_ = os.RemoveAll(filepath.Dir(binaryPath))
	os.Exit(code)
}

func compileBinary() (string, error) {
	tmpDir, err := os.MkdirTemp("", "cookies-test")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	binPath := filepath.Join(tmpDir, "cookies")
	cmd := exec.Command("go", "build", "-ldflags", "-X main.version=test -X main.commit=abc123", "-o", binPath, ".")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to build binary: %w", err)
	}

	return binPath, nil
}

type testServer struct {
	server   *http.Server
	listener net.Listener
	URL      string
}

func startTestServer(t *testing.T) *testServer {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			http.NotFound(w, r)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "test-cookie",
			Value:    "present",
			Expires:  time.Now().Add(24 * time.Hour),
			HttpOnly: false,
			Path:     "/",
		})
		http.SetCookie(w, &http.Cookie{
			Name:     "another-cookie",
			Value:    "also-present",
			Expires:  time.Now().Add(24 * time.Hour),
			HttpOnly: false,
			Path:     "/",
		})
		_, _ = fmt.Fprint(w, "Cookies set!")
	})

	listener, err := net.Listen("tcp", ":"+testPort)
	if err != nil {
		t.Fatalf("Failed to start listener: %v", err)
	}

	ts := &testServer{
		server:   &http.Server{Handler: mux},
		listener: listener,
		URL:      fmt.Sprintf("http://%s:%s", testDomain, testPort),
	}

	go func() {
		if err := ts.server.Serve(listener); err != http.ErrServerClosed {
			t.Logf("Server error: %v", err)
		}
	}()

	ts.waitReady(t)
	return ts
}

func (ts *testServer) waitReady(t *testing.T) {
	t.Helper()
	client := &http.Client{Timeout: 1 * time.Second}
	for range 30 {
		resp, err := client.Get(ts.URL)
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("Server did not start in time")
}

func (ts *testServer) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = ts.server.Shutdown(ctx)
}

func visitWithChrome(t *testing.T, url string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("Failed to get home directory: %v", err)
	}
	userDataDir := filepath.Join(homeDir, ".config", "google-chrome")
	cmd := exec.CommandContext(ctx, "google-chrome",
		"--headless=new",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--user-data-dir="+userDataDir,
		"--dump-dom",
		url,
	)
	_ = cmd.Run()
}

func visitWithFirefox(t *testing.T, url string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "firefox",
		"--headless",
		"--screenshot", "/dev/null",
		url,
	)
	_ = cmd.Run()
}

type cmdResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func runCookies(t *testing.T, args ...string) cmdResult {
	t.Helper()

	cmd := exec.Command(binaryPath, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()

	result := cmdResult{
		Stdout:   outBuf.String(),
		Stderr:   errBuf.String(),
		ExitCode: 0,
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
		}
	}

	return result
}

// =============================================================================
// NORMALIZATION FUNCTIONS
// =============================================================================

// normalizeLogTimestamp removes Go log.Fatal() timestamps
// Format: 2026/01/04 18:08:17 or 2026/01/04 18:08:17:59
func normalizeLogTimestamp(input string) string {
	re := regexp.MustCompile(`\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}(:\d{2})? `)
	return re.ReplaceAllString(input, "")
}

func normalizeJSON(t *testing.T, input string) string {
	t.Helper()

	var data map[string]any
	if err := json.Unmarshal([]byte(input), &data); err != nil {
		return input
	}

	normalized, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return input
	}

	return string(normalized)
}

func normalizeFullCookieJSON(t *testing.T, input string) string {
	t.Helper()

	var data map[string]map[string]any
	if err := json.Unmarshal([]byte(input), &data); err != nil {
		return input
	}

	for name, cookie := range data {
		if _, ok := cookie["Expires"]; ok {
			cookie["Expires"] = "NORMALIZED_TIMESTAMP"
		}
		if _, ok := cookie["Creation"]; ok {
			cookie["Creation"] = "NORMALIZED_TIMESTAMP"
		}
		data[name] = cookie
	}

	normalized, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return input
	}

	return string(normalized)
}

func normalizeCurlCommand(input string) string {
	re := regexp.MustCompile(`curl -H 'Cookie: ([^']+)' '([^']+)'`)
	matches := re.FindStringSubmatch(input)
	if len(matches) != 3 {
		return strings.TrimSpace(input)
	}

	cookies := strings.Split(matches[1], ";")
	sort.Strings(cookies)

	return fmt.Sprintf("curl -H 'Cookie: %s' '%s'", strings.Join(cookies, ";"), matches[2])
}

func normalizeDebugOutput(input string) string {
	output := input

	re := regexp.MustCompile(`/[^\s]+/cookies\.sqlite`)
	output = re.ReplaceAllString(output, "/PATH/cookies.sqlite")

	re = regexp.MustCompile(`/[^\s]+/Cookies`)
	output = re.ReplaceAllString(output, "/PATH/Cookies")

	re = regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}`)
	output = re.ReplaceAllString(output, "TIMESTAMP")

	re = regexp.MustCompile(`Size: \d+ bytes`)
	output = re.ReplaceAllString(output, "Size: N bytes")

	re = regexp.MustCompile(`Found \d+ cookie stores`)
	output = re.ReplaceAllString(output, "Found N cookie stores")

	// Different stores might log errors
	re = regexp.MustCompile(`(?i)store \d+`)
	output = re.ReplaceAllString(output, "Store N")

	// Cookie store error number fluctuates
	re = regexp.MustCompile(`, errors: \d+`)
	output = re.ReplaceAllString(output, ", errors: N")

	return output
}

func normalizeDebugJSON(t *testing.T, input string) string {
	t.Helper()

	var data map[string]string
	if err := json.Unmarshal([]byte(input), &data); err != nil {
		return input
	}

	for key, val := range data {
		re := regexp.MustCompile(`/[^\s:]+/(cookies\.sqlite|Cookies)`)
		data[key] = re.ReplaceAllString(val, "/PATH/$1")
	}

	normalized, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return input
	}

	return string(normalized)
}

func newGoldie(t *testing.T) *goldie.Goldie {
	return goldie.New(t,
		goldie.WithFixtureDir("testdata"),
		goldie.WithNameSuffix(".golden"),
		goldie.WithDiffEngine(goldie.ColoredDiff),
	)
}

// =============================================================================
// TESTS
// =============================================================================

type browserConfig struct {
	name  string
	visit func(t *testing.T, url string)
}

var browsers = []browserConfig{
	{
		name:  "chrome",
		visit: visitWithChrome,
	},
	{
		name:  "firefox",
		visit: visitWithFirefox,
	},
}

func TestHelp(t *testing.T) {
	g := newGoldie(t)
	result := runCookies(t, "-h")
	g.Assert(t, "help", []byte(result.Stdout))
}

func TestHelpNoFlags(t *testing.T) {
	g := newGoldie(t)
	result := runCookies(t)
	g.Assert(t, "help_no_flags", []byte(result.Stdout))
}

func TestVersion(t *testing.T) {
	g := newGoldie(t)
	result := runCookies(t, "-v")
	g.Assert(t, "version", []byte(result.Stdout))
}

func TestMissingDomain(t *testing.T) {
	g := newGoldie(t)
	result := runCookies(t, "-b", "firefox")
	output := normalizeLogTimestamp(result.Stdout + result.Stderr)
	g.Assert(t, "missing_domain", []byte(output))
}

func TestMutuallyExclusiveFlags(t *testing.T) {
	g := newGoldie(t)
	result := runCookies(t, "-b", "firefox", "-d", "localhost", "-c", "-n", "test")
	output := normalizeLogTimestamp(result.Stdout + result.Stderr)
	g.Assert(t, "mutually_exclusive_curl_name", []byte(output))
}

func TestBasicJSON(t *testing.T) {
	g := newGoldie(t)

	for _, browser := range browsers {
		t.Run(browser.name, func(t *testing.T) {
			ts := startTestServer(t)
			defer ts.Close()

			browser.visit(t, ts.URL)
			time.Sleep(500 * time.Millisecond)

			result := runCookies(t, "-b", browser.name, "-d", testDomain)
			if result.ExitCode != 0 {
				t.Fatalf("Command failed: %s", result.Stderr)
			}

			normalized := normalizeJSON(t, result.Stdout)
			g.Assert(t, "basic_json_"+browser.name, []byte(normalized))
		})
	}
}

func TestNameFlag(t *testing.T) {
	g := newGoldie(t)

	for _, browser := range browsers {
		t.Run(browser.name, func(t *testing.T) {
			ts := startTestServer(t)
			defer ts.Close()

			browser.visit(t, ts.URL)
			time.Sleep(500 * time.Millisecond)

			result := runCookies(t, "-b", browser.name, "-d", testDomain, "-n", "test-cookie")
			if result.ExitCode != 0 {
				t.Fatalf("Command failed: %s", result.Stderr)
			}

			g.Assert(t, "name_flag_"+browser.name, []byte(result.Stdout))
		})
	}
}

func TestCurlFlag(t *testing.T) {
	g := newGoldie(t)

	for _, browser := range browsers {
		t.Run(browser.name, func(t *testing.T) {
			ts := startTestServer(t)
			defer ts.Close()

			browser.visit(t, ts.URL)
			time.Sleep(500 * time.Millisecond)

			result := runCookies(t, "-b", browser.name, "-d", testDomain, "-c")
			if result.ExitCode != 0 {
				t.Fatalf("Command failed: %s", result.Stderr)
			}

			normalized := normalizeCurlCommand(result.Stdout)
			g.Assert(t, "curl_flag_"+browser.name, []byte(normalized))
		})
	}
}

func TestFullFlag(t *testing.T) {
	g := newGoldie(t)

	for _, browser := range browsers {
		t.Run(browser.name, func(t *testing.T) {
			ts := startTestServer(t)
			defer ts.Close()

			browser.visit(t, ts.URL)
			time.Sleep(500 * time.Millisecond)

			result := runCookies(t, "-b", browser.name, "-d", testDomain, "-f")
			if result.ExitCode != 0 {
				t.Fatalf("Command failed: %s", result.Stderr)
			}

			normalized := normalizeFullCookieJSON(t, result.Stdout)
			g.Assert(t, "full_flag_"+browser.name, []byte(normalized))
		})
	}
}

func TestExpiredFlag(t *testing.T) {
	g := newGoldie(t)

	for _, browser := range browsers {
		t.Run(browser.name, func(t *testing.T) {
			ts := startTestServer(t)
			defer ts.Close()

			browser.visit(t, ts.URL)
			time.Sleep(500 * time.Millisecond)

			result := runCookies(t, "-b", browser.name, "-d", testDomain, "-e")
			if result.ExitCode != 0 {
				t.Fatalf("Command failed: %s", result.Stderr)
			}

			normalized := normalizeJSON(t, result.Stdout)
			g.Assert(t, "expired_flag_"+browser.name, []byte(normalized))
		})
	}
}

func TestFullAndExpiredFlags(t *testing.T) {
	g := newGoldie(t)

	for _, browser := range browsers {
		t.Run(browser.name, func(t *testing.T) {
			ts := startTestServer(t)
			defer ts.Close()

			browser.visit(t, ts.URL)
			time.Sleep(500 * time.Millisecond)

			result := runCookies(t, "-b", browser.name, "-d", testDomain, "-f", "-e")
			if result.ExitCode != 0 {
				t.Fatalf("Command failed: %s", result.Stderr)
			}

			normalized := normalizeFullCookieJSON(t, result.Stdout)
			g.Assert(t, "full_and_expired_flags_"+browser.name, []byte(normalized))
		})
	}
}

func TestDebugFlag(t *testing.T) {
	g := newGoldie(t)

	for _, browser := range browsers {
		t.Run(browser.name, func(t *testing.T) {
			ts := startTestServer(t)
			defer ts.Close()

			browser.visit(t, ts.URL)
			time.Sleep(500 * time.Millisecond)

			result := runCookies(t, "-b", browser.name, "-d", testDomain, "-l")

			normalizedStderr := normalizeDebugOutput(result.Stderr)
			normalizedStdout := normalizeDebugStdout(t, result.Stdout)

			combined := fmt.Sprintf("STDERR:\n%s\nSTDOUT:\n%s", normalizedStderr, normalizedStdout)
			g.Assert(t, "debug_flag_"+browser.name, []byte(combined))
		})
	}
}

func TestDebugAndFullFlags(t *testing.T) {
	g := newGoldie(t)

	for _, browser := range browsers {
		t.Run(browser.name, func(t *testing.T) {
			ts := startTestServer(t)
			defer ts.Close()

			browser.visit(t, ts.URL)
			time.Sleep(500 * time.Millisecond)

			result := runCookies(t, "-b", browser.name, "-d", testDomain, "-l", "-f")

			normalizedStderr := normalizeDebugOutput(result.Stderr)
			normalizedStdout := normalizeDebugFullStdout(t, result.Stdout)

			combined := fmt.Sprintf("STDERR:\n%s\nSTDOUT:\n%s", normalizedStderr, normalizedStdout)
			g.Assert(t, "debug_and_full_flags_"+browser.name, []byte(combined))
		})
	}
}

func normalizeDebugStdout(t *testing.T, stdout string) string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	var parts []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var errData map[string]string
		if err := json.Unmarshal([]byte(line), &errData); err == nil {
			parts = append(parts, normalizeDebugJSON(t, line))
			continue
		}

		var cookieData map[string]any
		if err := json.Unmarshal([]byte(line), &cookieData); err == nil {
			parts = append(parts, normalizeJSON(t, line))
			continue
		}

		parts = append(parts, line)
	}

	return strings.Join(parts, "\n")
}

func normalizeDebugFullStdout(t *testing.T, stdout string) string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	var parts []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var errData map[string]string
		if err := json.Unmarshal([]byte(line), &errData); err == nil {
			parts = append(parts, normalizeDebugJSON(t, line))
			continue
		}

		var fullCookieData map[string]map[string]any
		if err := json.Unmarshal([]byte(line), &fullCookieData); err == nil {
			parts = append(parts, normalizeFullCookieJSON(t, line))
			continue
		}

		parts = append(parts, line)
	}

	return strings.Join(parts, "\n")
}
