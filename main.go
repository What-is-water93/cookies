package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/browserutils/kooky"
	_ "github.com/browserutils/kooky/browser/chrome"
	_ "github.com/browserutils/kooky/browser/firefox"
	"github.com/spf13/pflag"
)

var (
	browser           string
	curl              bool
	domain            string
	name              string
	fullCookieInfo    bool
	showExpired       bool
	help              bool
	cookieStoreErrors []string
	debug             bool
)

func printUsage() {
	fmt.Println("Obtain cookies from your browser stores")
	fmt.Println("\nUse with the following flags:")
	pflag.CommandLine.SortFlags = false
	pflag.PrintDefaults()

	os.Exit(0)
}

func parseFlags() error {
	pflag.StringVarP(&domain, "domain", "d", "", "cookie domain filter (partial). Required")
	pflag.StringVarP(&browser, "browser", "b", "chrome", "The browser you want to obtain cookies from")
	pflag.BoolVarP(&curl, "curl", "c", false, "outputs a curl command using all valid existing cookies for domain")
	pflag.BoolVarP(&showExpired, "expired", "e", false, "show expired cookies")
	pflag.BoolVarP(&fullCookieInfo, "full", "f", false, "outputs full information about each cookie")
	pflag.StringVarP(&name, "name", "n", "", "prints only the value of the given cookie (exact name match)")
	pflag.BoolVarP(&debug, "log-debug", "l", false, "logs cookie store errors, which are usually safe to ignore")
	pflag.BoolVarP(&help, "help", "h", false, "display usage information")
	pflag.Parse()

	if help || pflag.NFlag() == 0 {
		printUsage()
	}

	if domain == "" {
		return errors.New("flag domain is required, use either -d $DOMAIN or --domain $DOMAIN")
	}

	if curl && name != "" {
		return errors.New("flag 'curl' and flag 'name' are mutually exclusive")
	}

	return nil
}

func getCookies(browser string, domain string) ([]*kooky.Cookie, error) {
	var cookies []*kooky.Cookie
	cookieStores := kooky.FindAllCookieStores()

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
		}

		cookies = append(cookies, storeCookies...)
	}

	if cookies == nil {
		return nil, errors.New("no cookies for browser " + browser + " and domain " + domain + " found.")
	}

	return cookies, nil
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

func serializeFullCookieInfoToJson(cookies []*kooky.Cookie) (string, error) {
	cookiesMap := make(map[string]map[string]interface{})

	for _, item := range cookies {
		cookieMap := make(map[string]interface{})
		v := reflect.ValueOf(item).Elem()
		t := v.Type()

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
	var cookieParts []string

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

func formatStoreErrorsAsJson() (string, error) {
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

func run() error {
	err := parseFlags()
	if err != nil {
		return fmt.Errorf("incorrect flag usage: %w", err)
	}

	cookies, err := getCookies(browser, domain)
	if err != nil {
		return fmt.Errorf("failed to obtain cookies: %w", err)
	}
	if debug {
		jsonCookieStoreErrors, err := formatStoreErrorsAsJson()
		if err != nil {
			return fmt.Errorf("failed to marshal errors to json: %w", err)
		}
		fmt.Println(jsonCookieStoreErrors)
	}

	if name != "" {
		cookie_value, err := getCookieValue(cookies, name)
		if err != nil {
			return fmt.Errorf("failed to get value for cookie %s: %w", name, err)
		}
		fmt.Println(cookie_value)

	} else if curl {
		fmt.Println(
			createCurlCommand(cookies, domain),
		)

	} else if fullCookieInfo {
		cookieJson, err := serializeFullCookieInfoToJson(cookies)
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
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
