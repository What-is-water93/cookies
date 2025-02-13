package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"reflect"
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
}

func printUsage() {
	fmt.Println("Obtain cookies from your browser stores")
	fmt.Println("\nUse with the following flags:")
	pflag.CommandLine.SortFlags = false
	pflag.PrintDefaults()

	os.Exit(0)
}

func parseFlags(cfg *Config) error {
	pflag.StringVarP(&cfg.domain, "domain", "d", "", "cookie domain filter (partial). Required")
	pflag.StringVarP(&cfg.browser, "browser", "b", defaultBrowser, "The browser you want to obtain cookies from")
	pflag.BoolVarP(&cfg.curl, "curl", "c", false, "outputs a curl command using all valid existing cookies for domain")
	pflag.BoolVarP(&cfg.showExpired, "expired", "e", false, "show expired cookies")
	pflag.BoolVarP(&cfg.fullCookieInfo, "full", "f", false, "outputs full information about each cookie")
	pflag.BoolVarP(&cfg.fzfMode, "fuzzy", "z", false, "enable fuzzy search for all cookies of a domain (requires fzf)")
	pflag.StringVarP(&cfg.name, "name", "n", "", "prints only the value of the given cookie (exact name match)")
	pflag.BoolVarP(&cfg.debug, "log-debug", "l", false, "logs cookie store errors, which are usually safe to ignore")
	pflag.BoolVarP(&cfg.help, "help", "h", false, "display usage information")
	pflag.Parse()

	if cfg.help || pflag.NFlag() == 0 {
		printUsage()
	}

	if cfg.domain == "" {
		return errors.New("flag domain is required, use either -d $DOMAIN or --domain $DOMAIN")
	}

	if cfg.curl && cfg.name != "" {
		return errors.New("flag 'curl' and flag 'name' are mutually exclusive")
	}

	return nil
}

func getCookies(browser string, domain string, showExpired bool) ([]*kooky.Cookie, []string, error) {
	var cookies []*kooky.Cookie
	cookieStores := kooky.FindAllCookieStores()
	var cookieStoreErrors []string

	for _, store := range cookieStores {
		defer store.Close()

		if store.Browser() != browser {
			continue
		}

		var filters []kooky.Filter
		// only append the Valid filter if showExpired is false (default)
		if !showExpired {
			filters = append(filters, kooky.Valid)
		}

		filters = append(filters, kooky.DomainContains(domain))

		// Errors reading cookie stores are usually safe to ignore
		// An example would be a non existant cookie store for an unused chrome profile
		storeCookies, err := store.ReadCookies(filters...)
		if err != nil {
			cookieStoreErrors = append(cookieStoreErrors, err.Error())
			continue
		}

		if len(storeCookies) > 0 {
			cookies = append(cookies, storeCookies...)
		}
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

func fuzzyCookieSearch(cookies []*kooky.Cookie) (*kooky.Cookie, error) {
	cookieMap := make(map[string]*kooky.Cookie, len(cookies))
	for _, cookie := range cookies {
		cookieMap[cookie.Name] = cookie
	}

	cmd := exec.Command("fzf", "--height", "40%")
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
		defer stdin.Close()
		for name := range cookieMap {
			fmt.Fprintln(stdin, name)
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
	cookiesMap := make(map[string]map[string]interface{}, len(cookies))

	for _, item := range cookies {
		v := reflect.ValueOf(item).Elem()
		t := v.Type()
		cookieMap := make(map[string]interface{}, v.NumField())

		for i := 0; i < v.NumField(); i++ {
			field := t.Field(i)
			value := v.Field(i).Interface()
			// container for cookies are only used by firefox
			if field.Name == "Container" && browser != "firefox" {
				continue
			}

			cookieMap[field.Name] = value
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

func run(cfg Config) error {
	err := parseFlags(&cfg)
	if err != nil {
		return fmt.Errorf("incorrect flag usage: %w", err)
	}

	if cfg.fzfMode && !isFzfInstalled() {
		return fmt.Errorf("fzf is not in PATH. Please install fzf and add it to PATH to use fuzzy search mode")
	}

	cookies, cookieStoreErrors, err := getCookies(cfg.browser, cfg.domain, cfg.showExpired)
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
		selectedCookie, err := fuzzyCookieSearch(cookies)
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
		cookie_value, err := getCookieValue(cookies, cfg.name)
		if err != nil {
			return fmt.Errorf("failed to get value for cookie %s: %w", cfg.name, err)
		}
		fmt.Println(cookie_value)

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
	config := Config{}
	if err := run(config); err != nil {
		log.Fatal(err)
	}
}
