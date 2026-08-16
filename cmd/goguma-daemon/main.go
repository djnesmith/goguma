// Command goguma-daemon is the unprivileged background service.
//
// It runs as the user (a launchd LaunchAgent or a systemd user unit) and owns
// every policy decision: which jobs exist, when to wake, how long to hold, how
// long jobs typically take, and when the safety cutouts fire. It talks to the
// privileged helper only to block sleep and register an OS wake.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/junnam586/goguma/internal/daemon"
	"github.com/junnam586/goguma/internal/paths"
	"github.com/junnam586/goguma/internal/power"
	"github.com/junnam586/goguma/internal/store"
)

var version = "dev"

func main() {
	var (
		showVersion = flag.Bool("version", false, "print the version and exit")
		verbose     = flag.Bool("verbose", false, "log at debug level")
		logFile     = flag.String("log", "", "append logs to this file instead of stderr")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	layout := paths.MustResolve()
	if err := layout.EnsureDirs(); err != nil {
		fmt.Fprintf(os.Stderr, "goguma-daemon: can't prepare %s: %v\n", layout.StateDir, err)
		os.Exit(1)
	}

	log, closeLog, err := newLogger(*logFile, *verbose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "goguma-daemon: %v\n", err)
		os.Exit(1)
	}
	defer closeLog()

	st := store.New(layout)
	d := daemon.New(version, log, st, power.New())

	// SIGTERM is what launchd and systemd send on logout, shutdown, and
	// unload. Handling it is what allows the daemon to release its holds and
	// clear the global sleep block rather than leaving the machine unable to
	// sleep.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := d.Run(ctx); err != nil {
		log.Error("daemon exited with an error", "err", err)
		os.Exit(1)
	}
}

func newLogger(path string, verbose bool) (*slog.Logger, func(), error) {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	opts := &slog.HandlerOptions{Level: level}

	if path == "" {
		return slog.New(slog.NewTextHandler(os.Stderr, opts)), func() {}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("opening log file %s: %w", path, err)
	}
	return slog.New(slog.NewTextHandler(f, opts)), func() { f.Close() }, nil
}
