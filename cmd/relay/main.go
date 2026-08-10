// Command relay is the stateless message relay between a user's daemon and
// their phone.
//
// It has no database, no volume, and no persistent state of any kind: it
// matches two websockets and forwards frames between them. Deploy it, run it
// behind any host, or don't run one at all and point the app at a daemon over
// a LAN — the daemon speaks the same protocol either way.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lenajeremy/agentman/internal/relay"
)

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	secret := strings.TrimSpace(os.Getenv("AGENTMAN_RELAY_SECRET"))
	if secret == "" {
		// Refuse rather than generate one: a secret that changes on restart
		// would silently invalidate every paired device, which looks like a
		// mysterious logout rather than a configuration mistake.
		log.Error("AGENTMAN_RELAY_SECRET is required",
			"hint", "set it to a long random string; it signs device tokens and must be stable across restarts")
		os.Exit(1)
	}
	if len(secret) < 16 {
		log.Error("AGENTMAN_RELAY_SECRET is too short", "minimum", 16)
		os.Exit(1)
	}

	// Railway and most hosts supply PORT.
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := relay.NewServer(secret, version, log)
	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// No write timeout: these are long-lived websockets, and a deadline
		// here would sever an idle connection mid-session.
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	log.Info("relay listening", "port", port, "version", version, "storage", "none")
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server failed", "error", err)
		os.Exit(1)
	}
}
