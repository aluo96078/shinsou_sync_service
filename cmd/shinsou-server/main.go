// Command shinsou-server runs the Shinsou sync service.
//
// Usage:
//
//	shinsou-server [serve] [--listen addr] [--no-migrate]
//	shinsou-server migrate
//	shinsou-server admin create-invite [--max-uses N] [--expires 168h]
//	shinsou-server admin set-role <username> <user|moderator|admin>
//	shinsou-server admin reset-setup-code
//	shinsou-server version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/aluo96078/shinsou_sync_service/internal/config"
	"github.com/aluo96078/shinsou_sync_service/internal/db"
	"github.com/aluo96078/shinsou_sync_service/internal/httpapi"
	"github.com/aluo96078/shinsou_sync_service/internal/instance"
	"github.com/aluo96078/shinsou_sync_service/internal/jobs"
	"github.com/aluo96078/shinsou_sync_service/web"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "0.0.0-dev"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		return serve(args, stderr)
	case "migrate":
		return migrate(stdout)
	case "admin":
		return admin(args, stdout)
	case "version":
		fmt.Fprintln(stdout, version)
		return nil
	default:
		return fmt.Errorf("unknown command %q (want serve, migrate, admin or version)", cmd)
	}
}

func loadConfig() (config.Config, error) {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return cfg, err
	}
	return cfg, cfg.RequireDatabase()
}

func newLogger(cfg config.Config, w io.Writer) *slog.Logger {
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		level = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

func migrate(stdout io.Writer) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	n, err := db.Migrate(context.Background(), cfg.DatabaseURL, "")
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "applied %d migration(s)\n", n)
	return nil
}

func serve(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", "", "address to listen on (overrides SHINSOU_LISTEN)")
	noMigrate := fs.Bool("no-migrate", false, "do not run database migrations on start")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	log := newLogger(cfg, os.Stdout)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if !*noMigrate {
		n, err := db.Migrate(ctx, cfg.DatabaseURL, "")
		if err != nil {
			return err
		}
		log.Info("migrations applied", "count", n)
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL, "")
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := instance.EnsureSetupCode(ctx, pool, log); err != nil {
		return fmt.Errorf("setup code: %w", err)
	}
	srv, err := httpapi.New(ctx, cfg, pool, log, version, httpapi.WithWebFS(web.FS()))
	if err != nil {
		return err
	}
	defer srv.Close()

	runner := jobs.New(pool, jobs.Options{
		Interval:           cfg.JobInterval,
		TombstoneRetention: cfg.TombstoneRetention,
		BatchRetention:     cfg.BatchRetention,
		// Expired mail tokens stay for a week before they are purged.
		EmailTokenRetention: 7 * 24 * time.Hour,
	}, log)
	go runner.Run(ctx)

	// Long-lived streams derive their context from the base context, so cancelling it on
	// shutdown ends them instead of stalling Shutdown until its timeout.
	baseCtx, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()
	hs := srv.HTTPServer()
	hs.BaseContext = func(net.Listener) context.Context { return baseCtx }

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Listen, "version", version, "registration", cfg.Registration, "email", cfg.EmailAvailable())
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
		close(errc)
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	cancelBase()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return hs.Shutdown(shutdownCtx)
}

func admin(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("admin: want create-invite, set-role or reset-setup-code")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, cfg.DatabaseURL, "")
	if err != nil {
		return err
	}
	defer pool.Close()

	switch args[0] {
	case "create-invite":
		fs := flag.NewFlagSet("create-invite", flag.ContinueOnError)
		maxUses := fs.Int("max-uses", 1, "how many accounts the invite can create")
		expires := fs.Duration("expires", 7*24*time.Hour, "validity period; 0 for no expiry")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		code, err := instance.CreateInvite(ctx, pool, *maxUses, *expires)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, code)
	case "set-role":
		if len(args) != 3 {
			return errors.New("usage: admin set-role <username> <user|moderator|admin>")
		}
		if err := instance.SetRole(ctx, pool, args[1], args[2]); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s is now %s\n", args[1], args[2])
	case "reset-setup-code":
		code, err := instance.ResetSetupCode(ctx, pool)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, code)
	default:
		return fmt.Errorf("admin: unknown subcommand %q", args[0])
	}
	return nil
}
