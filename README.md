This is a thin wrapper around the [browserutils/kooky](https://github.com/browserutils/kooky/) library, they did all the hard work.  
My tool only adds a CLI I like around the library, e.g. fuzzy selection and flags I find more intuitive.

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

# Known Issues
## Chrome
- There might be some sites with encrypted cookie values which do contain partial AES blocks of less than 16 bytes, resulting in a crypto/cipher panic and failed decryption for the specific cookie store. I was not able to figure out why I have those malformed cookies. 
My workaround is to avoid using e.g. single letter domain filters, since those often catch malformed cookies.
  - Showing expired cookies too (`-e`) has a higher chance of running into this error.
