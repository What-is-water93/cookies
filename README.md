# Build the binary
In the directory:
1. Install go (`mise install`/`asdf install`)
2. Run `go mod download` to download the required libraries
3. Run `go build .` (or`CGO_ENABLED=0 go build .` for a statically linked binary)
4. Optionally: Move the binary to a location in your $PATH

# Usage:
`./cookie -d "$DOMAINPATTERN"` will return  all chrome cookies for domains containing the domainpattern. The `-d` flag is required.  
Firefox is also supported, and you can also output a curl command containing all the cookies, or print the value of a specific given cookie for the given domain.
For further info run `cookie` or `cookie -h` to show infos about supported flags.
