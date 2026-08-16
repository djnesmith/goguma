// Command goguma-advisory generates the signing keypair and signs a feed.
//
// Run by the author, never by a user, and never in CI with the key in an
// environment variable if that can be avoided: the private half is the one
// thing that decides whether a notice shown to every install is genuine.
//
//	goguma-advisory keygen
//	    prints a new keypair. The public half goes into the build via
//	    -ldflags; the private half goes somewhere only you can reach.
//
//	goguma-advisory sign --key <file> < feed.json > advisories.json
//	    fills in the signature field.
//
//	goguma-advisory verify --pub <base64> < advisories.json
//	    checks a published file the way an installed goguma would.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/junnam586/goguma/internal/advisory"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "keygen":
		keygen()
	case "sign":
		sign(os.Args[2:])
	case "verify":
		verify(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `goguma-advisory: sign the notices goguma shows its users

  goguma-advisory keygen
  goguma-advisory sign --key <private-key-file> < feed.json > advisories.json
  goguma-advisory verify --pub <base64-public-key> < advisories.json
`)
	os.Exit(2)
}

func keygen() {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		die(err)
	}
	fmt.Printf("public  (compile into the binary, safe to publish):\n%s\n\n",
		base64.StdEncoding.EncodeToString(pub))
	fmt.Printf("private (keep this secret, it signs what your users are told):\n%s\n\n",
		base64.StdEncoding.EncodeToString(priv))
	fmt.Print(`Build with the public half:
  go build -ldflags "-X github.com/junnam586/goguma/internal/advisory.publicKeyB64=<public>" ./cmd/...

Anyone holding the private half can put words in goguma's mouth on every
install. It cannot change any setting, run anything, or read anything, but it
can display a sentence. Store it accordingly.
`)
}

func sign(args []string) {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	keyFile := fs.String("key", "", "file holding the base64 private key")
	_ = fs.Parse(args)
	if *keyFile == "" {
		usage()
	}
	raw, err := os.ReadFile(*keyFile)
	if err != nil {
		die(err)
	}
	priv, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		die(fmt.Errorf("%s does not hold a base64 ed25519 private key", *keyFile))
	}

	var f advisory.Feed
	if err := json.NewDecoder(os.Stdin).Decode(&f); err != nil {
		die(fmt.Errorf("reading the feed on stdin: %w", err))
	}
	f.Signature = advisory.Sign(f, ed25519.PrivateKey(priv))

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		die(err)
	}
}

func verify(args []string) {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	pubB64 := fs.String("pub", "", "base64 public key")
	_ = fs.Parse(args)
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(*pubB64))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		die(fmt.Errorf("--pub is not a base64 ed25519 public key"))
	}
	var f advisory.Feed
	if err := json.NewDecoder(os.Stdin).Decode(&f); err != nil {
		die(err)
	}
	if err := f.Verify(ed25519.PublicKey(pub)); err != nil {
		die(err)
	}
	fmt.Fprintln(os.Stderr, "signature ok")
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "goguma-advisory:", err)
	os.Exit(1)
}
