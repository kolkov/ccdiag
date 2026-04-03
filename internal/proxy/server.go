package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"time"
)

// Config holds proxy server configuration.
type Config struct {
	Port     int
	Upstream string // e.g. "https://api.anthropic.com"
	Verbose  bool
	LogDir   string // defaults to ~/.ccdiag/proxy
}

// DefaultConfig returns configuration with sensible defaults.
func DefaultConfig() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Port:     9119,
		Upstream: "https://api.anthropic.com",
		LogDir:   filepath.Join(home, ".ccdiag", "proxy"),
	}
}

// Run starts the proxy server and blocks until SIGINT/SIGTERM.
func Run(ctx context.Context, cfg Config) error {
	upstream, err := url.Parse(cfg.Upstream)
	if err != nil {
		return fmt.Errorf("parse upstream URL: %w", err)
	}

	logger, err := NewLogger(cfg.LogDir)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer logger.Close()

	// Build the reverse proxy.
	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = upstream.Scheme
			req.URL.Host = upstream.Host
			req.Host = upstream.Host
		},
	}

	mw := &middleware{
		upstream: upstream,
		logger:   logger,
		verbose:  cfg.Verbose,
	}

	handler := mw.wrap(rp)

	addr := fmt.Sprintf(":%d", cfg.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
		// Generous timeouts for LLM API calls which can be slow.
		ReadHeaderTimeout: 30 * time.Second,
		// No WriteTimeout — streaming responses can take minutes.
		IdleTimeout: 120 * time.Second,
	}

	// Graceful shutdown on signals.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	// Start listening.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	fmt.Fprintf(os.Stderr, "ccdiag proxy listening on %s -> %s\n", addr, cfg.Upstream)
	if cfg.Verbose {
		fmt.Fprintf(os.Stderr, "verbose mode: logging all requests to stderr\n")
	}
	fmt.Fprintf(os.Stderr, "traffic log: %s/traffic.jsonl\n", cfg.LogDir)
	fmt.Fprintf(os.Stderr, "press Ctrl+C to stop\n\n")

	// Serve in a goroutine.
	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	// Wait for shutdown signal or server error.
	select {
	case <-ctx.Done():
		fmt.Fprintf(os.Stderr, "\nshutting down...\n")
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}
