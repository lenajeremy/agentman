// Command relay is the stateless message relay between a user's daemon and
// their phone.
//
// It has no database or persistent transcript state: it matches two websockets
// and forwards frames between them. Payloads are not end-to-end encrypted, so
// the relay operator remains a live trust boundary.
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

func isTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func main() {

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	secret := strings.TrimSpace(os.Getenv("AGENTMAN_RELAY_SECRET"))
	if secret == "" {
		log.Error("AGENTMAN_RELAY_SECRET is required",
			"hint", "set it to a long random string; it signs device tokens and must be stable across restarts")
		os.Exit(1)
	}
	if len(secret) < 16 {
		log.Error("AGENTMAN_RELAY_SECRET is too short", "minimum", 16)
		os.Exit(1)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Railway overwrites X-Forwarded-For, so it can be believed there; a relay
	// exposed straight to the internet receives whatever the client sent.
	trustProxy := isTruthy(os.Getenv("AGENTMAN_TRUST_PROXY"))
	if !trustProxy {
		log.Info("rate limiting by socket address",
			"hint", "set AGENTMAN_TRUST_PROXY=1 when a proxy in front overwrites X-Forwarded-For")
	}

	server := relay.NewServer(secret, version, log, trustProxy)
	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    16 * 1024,
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
