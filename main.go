package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"

	"github.com/browserutils/kooky"
	_ "github.com/browserutils/kooky/browser/chrome"
	_ "github.com/browserutils/kooky/browser/firefox"
	"github.com/spf13/pflag"
)

const (
	defaultBrowser = "chrome"
)

var (
	version     = "dev"
	commit      = "unknown"
	ErrExitZero = errors.New("exit zero")
)

type Config struct {
	browser        string
	curl           bool
	domain         string
	fzfMode        bool
	name           string
	fullCookieInfo bool
	showExpired    bool
	help           bool
	debug          bool
	version        bool
}

func printUsage() {
	fmt.Println("Obtain cookies from your browser stores")
	fmt.Printf("Version %s (commit: %s)\n", version, commit)
	fmt.Println("\nUse with the following flags:")
	pflag.CommandLine.SortFlags = false
	pflag.CommandLine.SetOutput(os.Stdout)
	pflag.PrintDefaults()
}

func parseFlags(cfg *Config) error {
	pflag.StringVarP(&cfg.domain, "domain", "d", "", "cookie domain filter (partial). Required")
	pflag.StringVarP(&cfg.browser, "browser", "b", defaultBrowser, "The browser you want to obtain cookies from")
	pflag.BoolVarP(&cfg.curl, "curl", "c", false, "outputs a curl command using all valid existing cookies for domain")
	pflag.BoolVarP(&cfg.showExpired, "expired", "e", false, "show expired cookies")
	pflag.BoolVarP(&cfg.fullCookieInfo, "full", "f", false, "outputs full information about each cookie")
	pflag.BoolVarP(&cfg.fzfMode, "fuzzy", "z", false, "enable fuzzy search for all cookies of a domain (requires fzf)")
	pflag.StringVarP(&cfg.name, "name", "n", "", "prints only the value of the given cookie (exact name match)")
	pflag.BoolVarP(&cfg.version, "version", "v", false, "display version information")
	pflag.BoolVarP(&cfg.debug, "log-debug", "l", false, "logs cookie store errors, which are usually safe to ignore")

	pflag.BoolVarP(&cfg.help, "help", "h", false, "display usage information")
	pflag.Parse()

	if cfg.help || pflag.NFlag() == 0 {
		printUsage()
		return ErrExitZero
	}

	if cfg.version {
		fmt.Printf("cookies version %s (commit: %s)\n", version, commit)
		return ErrExitZero
	}

	if cfg.domain == "" {
		return errors.New("flag domain is required, use either -d $DOMAIN or --domain $DOMAIN")
	}

	if cfg.curl && cfg.name != "" {
		return errors.New("flag 'curl' and flag 'name' are mutually exclusive")
	}

	return nil
}

func debugCookieStore(store kooky.CookieStore, storeNum int) {
	fmt.Fprintf(os.Stderr, "Debug: Store %d Details:\n", storeNum)
	fmt.Fprintf(os.Stderr, "  Browser: %s\n", store.Browser())
	fmt.Fprintf(os.Stderr, "  File: %s\n", store.FilePath())

	if info, err := os.Stat(store.FilePath()); err == nil {
		fmt.Fprintf(os.Stderr, "  Size: %d bytes\n", info.Size())
		fmt.Fprintf(os.Stderr, "  Modified: %s\n", info.ModTime().Format("2006-01-02 15:04:05"))
	} else {
		fmt.Fprintf(os.Stderr, "  Error accessing file: %v\n", err)
	}
}

// readCookiesFromStore reads cookies from a single cookie store, closing it when done.
// Returns nil, nil if the store doesn't match the requested browser.
// Returns nil, err if the store could not be read (non-fatal, caller decides).
func readCookiesFromStore(ctx context.Context, store kooky.CookieStore, storeNum int, browser string, domain string, showExpired bool, debug bool) ([]*kooky.Cookie, error) {
	defer func() {
		if err := store.Close(); err != nil && debug {
			fmt.Fprintf(os.Stderr, "Debug: Error closing store %d: %v\n", storeNum, err)
		}
	}()

	if store.Browser() != browser {
		return nil, nil
	}

	if debug {
		debugCookieStore(store, storeNum)
	}

	var filters []kooky.Filter
	// only append the Valid filter if showExpired is false (default)
	if !showExpired {
		filters = append(filters, kooky.Valid)
	}
	filters = append(filters, kooky.DomainContains(domain))

	if debug {
		fmt.Fprintf(os.Stderr, "Debug: Reading cookies from store %d\n", storeNum)
	}

	// Errors reading cookie stores are usually safe to ignore.
	// An example would be a non-existent cookie store for an unused chrome profile.
	storeCookies, err := store.TraverseCookies(filters...).ReadAllCookies(ctx)
	if err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "Debug: Store %d error: %v\n", storeNum, err)
		}
		return nil, err
	}

	if debug {
		fmt.Fprintf(os.Stderr, "Debug: Store %d returned %d cookies\n", storeNum, len(storeCookies))
	}

	return storeCookies, nil
}

func getCookies(ctx context.Context, browser string, domain string, showExpired bool, debug bool) ([]*kooky.Cookie, []string, error) {
	if debug {
		fmt.Fprintf(os.Stderr, "Debug: Starting getCookies for browser=%s, domain=%s, showExpired=%v\n", browser, domain, showExpired)
	}

	var cookies []*kooky.Cookie
	var cookieStoreErrors []string
	cookieStores := kooky.FindAllCookieStores(ctx)

	if debug {
		fmt.Fprintf(os.Stderr, "Debug: Found %d cookie stores\n", len(cookieStores))
	}

	for i, store := range cookieStores {
		if err := ctx.Err(); err != nil {
			return nil, cookieStoreErrors, err
		}

		storeNum := i + 1
		storeCookies, err := readCookiesFromStore(ctx, store, storeNum, browser, domain, showExpired, debug)
		if err != nil {
			cookieStoreErrors = append(cookieStoreErrors, err.Error())
			continue
		}

		if len(storeCookies) > 0 {
			cookies = append(cookies, storeCookies...)
			if debug {
				fmt.Fprintf(os.Stderr, "Debug: Added %d cookies from store %d\n", len(storeCookies), storeNum)
			}
		}
	}

	if debug {
		fmt.Fprintf(os.Stderr, "Debug: Total cookies found: %d, errors: %d\n", len(cookies), len(cookieStoreErrors))
	}

	if cookies == nil {
		return nil, cookieStoreErrors, fmt.Errorf("no cookies found for browser %s and domain %s", browser, domain)
	}

	return cookies, cookieStoreErrors, nil
}

func isFzfInstalled() bool {
	_, err := exec.LookPath("fzf")
	return err == nil
}

func fuzzyCookieSearch(ctx context.Context, cookies []*kooky.Cookie, debug bool) (*kooky.Cookie, error) {
	cookieMap := make(map[string]*kooky.Cookie, len(cookies))
	for _, cookie := range cookies {
		cookieMap[cookie.Name] = cookie
	}

	cmd := exec.CommandContext(ctx, "fzf", "--height", "99%")
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start fzf: %w", err)
	}

	go func() {
		defer func() {
			if err := stdin.Close(); err != nil && debug {
				fmt.Fprintf(os.Stderr, "Debug: Error closing fzf stdin: %v\n", err)
			}
		}()
		for name := range cookieMap {
			if _, err := fmt.Fprintln(stdin, name); err != nil && debug {
				fmt.Fprintf(os.Stderr, "Debug: Error writing to fzf stdin: %v\n", err)
			}
		}
	}()

	output, err := io.ReadAll(stdout)
	if err != nil {
		return nil, fmt.Errorf("failed to read fzf output: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("fzf returned %w", err)
	}

	selected := strings.TrimSpace(string(output))

	return cookieMap[selected], nil
}

func serializeCookiesToJson(cookies []*kooky.Cookie) (string, error) {
	cookiesMap := make(map[string]string, len(cookies))

	for _, item := range cookies {
		cookiesMap[item.Name] = item.Value
	}

	cookiesJsonBytes, err := json.Marshal(cookiesMap)
	if err != nil {
		return "", err
	}

	return string(cookiesJsonBytes), nil
}

func serializeFullCookieInfoToJson(cookies []*kooky.Cookie, browser string) (string, error) {
	cookiesMap := make(map[string]map[string]any, len(cookies))

	for _, item := range cookies {
		cookieMap := make(map[string]any)

		cookieMap["Name"] = item.Name
		cookieMap["Value"] = item.Value
		cookieMap["Path"] = item.Path
		cookieMap["Domain"] = item.Domain
		cookieMap["Expires"] = item.Expires.Unix()
		cookieMap["RawExpires"] = item.RawExpires
		cookieMap["MaxAge"] = item.MaxAge
		cookieMap["Secure"] = item.Secure
		cookieMap["HttpOnly"] = item.HttpOnly
		cookieMap["SameSite"] = item.SameSite
		cookieMap["Raw"] = item.Raw
		cookieMap["Unparsed"] = item.Unparsed
		cookieMap["Creation"] = item.Creation.Unix()

		// Container field is only used by firefox
		if browser == "firefox" {
			cookieMap["Container"] = item.Container
		}
		cookiesMap[item.Name] = cookieMap
	}

	cookiesJsonBytes, err := json.Marshal(cookiesMap)
	if err != nil {
		return "", err
	}

	return string(cookiesJsonBytes), nil
}

func createCurlCommand(cookies []*kooky.Cookie, domain string) string {
	cookieParts := make([]string, 0, len(cookies))

	for _, cookie := range cookies {
		cookieParts = append(cookieParts, fmt.Sprintf("%s=%s", cookie.Name, cookie.Value))
	}

	cookieString := strings.Join(cookieParts, ";")

	return fmt.Sprintf("curl -H 'Cookie: %s' 'https://%s'", cookieString, domain)
}

func getCookieValue(cookies []*kooky.Cookie, name string) (string, error) {
	for _, cookie := range cookies {
		if name == cookie.Name {
			if cookie.Value == "" {
				return "", errors.New("cookie exists but has an empty value")
			}
			return cookie.Value, nil
		}
	}
	return "", errors.New("cookie does not exist")
}

func formatStoreErrorsAsJson(cookieStoreErrors []string) (string, error) {
	jsonErrors := make(map[string]string, len(cookieStoreErrors))
	for i, v := range cookieStoreErrors {
		key := strconv.Itoa(i + 1)
		jsonErrors[key] = v
	}

	jsonErrorsString, err := json.Marshal(jsonErrors)
	if err != nil {
		return "", err
	}

	return string(jsonErrorsString), nil
}

func run(ctx context.Context, cfg Config) error {
	err := parseFlags(&cfg)
	if err == ErrExitZero {
		return nil
	}
	if err != nil {
		return fmt.Errorf("incorrect flag usage: %w", err)
	}

	if cfg.fzfMode && !isFzfInstalled() {
		return fmt.Errorf("fzf is not in PATH. Please install fzf and add it to PATH to use fuzzy search mode")
	}

	cookies, cookieStoreErrors, err := getCookies(ctx, cfg.browser, cfg.domain, cfg.showExpired, cfg.debug)
	if err != nil {
		return fmt.Errorf("failed to obtain cookies: %w", err)
	}
	if cfg.debug {
		jsonCookieStoreErrors, err := formatStoreErrorsAsJson(cookieStoreErrors)
		if err != nil {
			return fmt.Errorf("failed to marshal errors to json: %w", err)
		}
		fmt.Println(jsonCookieStoreErrors)
	}

	if cfg.fzfMode {
		selectedCookie, err := fuzzyCookieSearch(ctx, cookies, cfg.debug)
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 130 {
				fmt.Println("cookie selection cancelled")
				return nil
			}
			return fmt.Errorf("fuzzy search failed: %w", err)
		}
		fmt.Println(selectedCookie.Value)
		return nil
	}

	if cfg.name != "" {
		cookieValue, err := getCookieValue(cookies, cfg.name)
		if err != nil {
			return fmt.Errorf("failed to get value for cookie %s: %w", cfg.name, err)
		}
		fmt.Println(cookieValue)

	} else if cfg.curl {
		fmt.Println(
			createCurlCommand(cookies, cfg.domain),
		)

	} else if cfg.fullCookieInfo {
		cookieJson, err := serializeFullCookieInfoToJson(cookies, cfg.browser)
		if err != nil {
			return fmt.Errorf("failed to create JSON: %w", err)
		}
		fmt.Println(cookieJson)
	} else {
		cookieJson, err := serializeCookiesToJson(cookies)
		if err != nil {
			return fmt.Errorf("failed to create JSON: %w", err)
		}
		fmt.Println(cookieJson)
	}
	return nil
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	config := Config{}
	if err := run(ctx, config); err != nil {
		log.Fatal(err)
	}
}
