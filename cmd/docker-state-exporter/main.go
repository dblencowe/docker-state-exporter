// Package main implements the docker-state-exporter binary: a Prometheus
// exporter that scrapes container state, health, and lifecycle timestamps
// from the local Docker daemon.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const readinessPath = "/-/ready"

// Injected at build time via -ldflags="-X main.version=... -X main.commit=... -X main.date=...".
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

type cliFlags struct {
	listenAddress string
	metricsPath   string
	cacheTTL      time.Duration
	hostLabel     string
	logLevel      string
	logFormat     string
	healthcheck   bool
	showVersion   bool
}

func parseFlags() cliFlags {
	defaults := cliFlags{
		listenAddress: ":8080",
		metricsPath:   "/metrics",
		cacheTTL:      time.Second,
		logLevel:      "info",
		logFormat:     "json",
	}

	cfg := defaults
	flag.StringVar(&cfg.listenAddress, "listen-address", defaults.listenAddress, "Address to listen on for HTTP requests.")
	flag.StringVar(&cfg.metricsPath, "metrics-path", defaults.metricsPath, "URL path under which to expose Prometheus metrics.")
	flag.DurationVar(&cfg.cacheTTL, "cache-ttl", defaults.cacheTTL, "How long to reuse a snapshot of docker inspect results.")
	flag.StringVar(&cfg.hostLabel, "host-label", "", "Value for the 'host' Prometheus label. Empty (default) omits the label entirely.")
	flag.StringVar(&cfg.logLevel, "log-level", defaults.logLevel, "Log level: debug, info, warn, error.")
	flag.StringVar(&cfg.logFormat, "log-format", defaults.logFormat, "Log format: json or text.")
	flag.BoolVar(&cfg.healthcheck, "healthcheck", false, "Probe the readiness endpoint of a running instance and exit 0/1. Used by the Dockerfile HEALTHCHECK.")
	flag.BoolVar(&cfg.showVersion, "version", false, "Print version information and exit.")
	flag.Parse()
	return cfg
}

func main() {
	cfg := parseFlags()

	if cfg.showVersion {
		fmt.Printf("docker-state-exporter %s (commit %s, built %s)\n", version, commit, date)
		return
	}

	if cfg.healthcheck {
		if err := runHealthcheck(cfg.listenAddress, cfg.metricsPath); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	logger, err := newLogger(cfg.logLevel, cfg.logFormat)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	slog.SetDefault(logger)

	if err := run(cfg, logger); err != nil {
		logger.Error("fatal", "error", err.Error())
		os.Exit(1)
	}
}

func run(cfg cliFlags, logger *slog.Logger) error {
	cli, err := newDockerClient()
	if err != nil {
		return fmt.Errorf("creating docker client: %w", err)
	}
	defer func() { _ = cli.Close() }()

	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(pingCtx); err != nil {
		return fmt.Errorf("pinging docker daemon (check DOCKER_HOST and socket permissions): %w", err)
	}

	collector := newCollector(collectorOptions{
		Client:    cli,
		Logger:    logger,
		HostLabel: cfg.hostLabel,
		CacheTTL:  cfg.cacheTTL,
	})

	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewBuildInfoCollector())
	registry.MustRegister(collector)

	mux := http.NewServeMux()
	mux.HandleFunc("/", indexHandler(cfg.metricsPath))
	mux.HandleFunc("/-/healthy", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "up")
	})
	mux.HandleFunc(readinessPath, readinessHandler(cli))
	mux.Handle(cfg.metricsPath, promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
		EnableOpenMetrics: true,
		Registry:          registry,
	}))

	server := &http.Server{
		Addr:              cfg.listenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("server listening",
			"address", cfg.listenAddress,
			"metrics_path", cfg.metricsPath,
			"version", version,
		)
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, os.Interrupt)

	select {
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	case sig := <-quit:
		logger.Info("shutdown signal received", "signal", sig.String())
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("server shutdown complete")
	return nil
}

func indexHandler(metricsPath string) http.HandlerFunc {
	body := fmt.Sprintf(`<html>
<head><title>Docker State Exporter</title></head>
<body>
<h1>Docker State Exporter</h1>
<p><a href="%s">Metrics</a></p>
<p><a href="/-/healthy">Liveness</a> &middot; <a href="%s">Readiness</a></p>
</body>
</html>
`, metricsPath, readinessPath)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}
}

func readinessHandler(cli DockerClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if _, err := cli.Ping(ctx); err != nil {
			http.Error(w, "docker unreachable: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, "ready")
	}
}

func newLogger(level, format string) (*slog.Logger, error) {
	lvl, err := parseLevel(level)
	if err != nil {
		return nil, err
	}
	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	switch strings.ToLower(format) {
	case "json", "":
		handler = slog.NewJSONHandler(os.Stdout, opts)
	case "text":
		handler = slog.NewTextHandler(os.Stdout, opts)
	default:
		return nil, fmt.Errorf("invalid log format %q (want 'json' or 'text')", format)
	}
	return slog.New(handler), nil
}

func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level %q", s)
	}
}
