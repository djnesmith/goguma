// Command goguma is the user-facing CLI.
//
// It never performs privileged work itself and never touches power management
// directly; everything goes through the daemon, which owns all policy. That
// keeps this binary safe to run from anywhere, including a cron job.
package main

import (
	"os"

	"github.com/junnam586/goguma/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	cli.Version = version
	os.Exit(cli.Main(os.Args[1:]))
}
