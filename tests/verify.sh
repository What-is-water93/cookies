#!/bin/bash
set -e

./testserver &
SERVER_PID=$!
sleep 1

TEST_DOMAIN="localhost"
URL="http://${TEST_DOMAIN}:8080"

check_cookie() {
    local browser=$1
    echo "Checking for cookies in $browser for domain $TEST_DOMAIN..."
    OUTPUT=$(./cookies -b "$browser" -d "$TEST_DOMAIN" 2>&1 || true)

    if echo "$OUTPUT" | grep -q "test-cookie"; then
        echo "SUCCESS: Found cookie in $browser"
        return 0
    fi

    echo "FAILURE: Did not find cookie in $browser."
    echo "Last output: $OUTPUT"
    return 1
}

echo "=== Testing with Chrome ==="
BROWSER="google-chrome"
USER_DATA_DIR="/root/.config/google-chrome"

if ! command -v "$BROWSER" >/dev/null; then
    echo "No $BROWSER binary found"
    exit 1
fi

mkdir -p "$USER_DATA_DIR"

timeout 10s $BROWSER --headless=new --no-sandbox --disable-dev-shm-usage --user-data-dir="$USER_DATA_DIR" --dump-dom "$URL" > /dev/null 2>&1

if check_cookie "chrome"; then
    echo "Chrome test passed."
else
    CHROME_FAILED=true
fi

echo "=== Testing with Firefox ==="
if ! command -v firefox >/dev/null; then
    echo "No firefox binary found"
    exit 1
fi

timeout 10s firefox --headless --screenshot "$URL" > /dev/null 2>&1 || true

if check_cookie "firefox"; then
    echo "Firefox test passed."
else
    FIREFOX_FAILED=true
fi

if [ "$CHROME_FAILED" = true ] || [ "$FIREFOX_FAILED" = true ]; then
    exit 1
fi

echo "All tests passed!"
exit 0
