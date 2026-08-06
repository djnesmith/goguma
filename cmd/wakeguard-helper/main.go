// Command wakeguard-helper is the privileged component.
//
// It runs as root (a launchd LaunchDaemon or a systemd system unit) and does
// exactly two things on request: block or unblock sleep, and register or
// cancel an OS wake. It holds no schedules, no job definitions, and no
// policy — every decision is made by the unprivileged daemon.
//
// This is the entire elevated-privilege surface of WakeGuard, and it is meant
// to stay small enough to read end to end before trusting it.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/junnam/wakeguard/internal/helper"
	"github.com/junnam/wakeguard/internal/ipc"
	"github.com/junnam/wakeguard/internal/paths"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var (
		showVersion = flag.Bool("version", false, "print the version and exit")
		ownerUID    = flag.Int("owner-uid", -1, "uid permitted to control the helper (defaults to SUDO_UID, else any non-root is rejected)")
		socketPath  = flag.String("socket", paths.HelperSocket, "socket to listen on")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if os.Geteuid() != 0 {
		log.Error("the helper must run as root; it exists to perform the one operation the daemon cannot")
		os.Exit(1)
	}

	owner := resolveOwner(*ownerUID, log)

	svc := helper.New(version, log)

	// Clear any block stranded by a previous run before accepting requests.
	// The unit is configured to start at boot so this happens before login.
	svc.Recover()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv, err := ipc.Listen(*socketPath, 0o600, svc.Handle,
		// The trust boundary. Only the owning user and root may reach the
		// privileged operations; the uid comes from the kernel, never from
		// anything the client sends.
		ipc.WithAuthz(ipc.AllowOwnerOrRoot(owner)),
		// The helper binds as root, so without this the socket is root-owned at
		// mode 0600 and the daemon cannot open it at all — it never reaches the
		// authz check above.
		ipc.WithSocketOwner(int(owner)),
		ipc.WithLogger(log),
	)
	if err != nil {
		log.Error("could not listen", "socket", *socketPath, "err", err)
		os.Exit(1)
	}

	go svc.RunDeadMan(ctx)

	log.Info("helper started", "version", version, "socket", *socketPath, "owner_uid", owner)
	if err := srv.Serve(ctx); err != nil {
		log.Error("server stopped", "err", err)
	}

	// Clear the sleep block on the way out. `disablesleep` is global and is
	// not released when this process exits, so skipping this would leave the
	// machine unable to sleep.
	svc.Shutdown()
	srv.Close()
	log.Info("helper stopped")
}

// resolveOwner determines which uid may control the helper.
//
// Preference order: an explicit flag (written into the unit file at install
// time), then SUDO_UID (the human who ran the installer). Falling back to
// root-only is deliberate: a helper that accepted any uid would let any local
// account pin the machine awake.
func resolveOwner(flagUID int, log *slog.Logger) uint32 {
	if flagUID >= 0 {
		return uint32(flagUID)
	}
	if s := os.Getenv("SUDO_UID"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			return uint32(n)
		}
	}
	log.Warn("no owner uid was supplied; only root will be able to control this helper")
	return 0
}
