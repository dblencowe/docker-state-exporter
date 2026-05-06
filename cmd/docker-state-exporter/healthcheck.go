package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// runHealthcheck performs a self-call to the readiness endpoint and exits 0
// on success, 1 on failure. Used by the Dockerfile HEALTHCHECK because the
// distroless base image has neither curl nor wget.
func runHealthcheck(listenAddress, metricsPath string) error {
	host, port, err := splitHostPort(listenAddress)
	if err != nil {
		return fmt.Errorf("parsing listen address %q: %w", listenAddress, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}

	url := fmt.Sprintf("http://%s:%s%s", host, port, readinessPath)
	_ = metricsPath // accepted for symmetry; readiness is the canonical probe

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling %s: %w", url, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: status=%d", resp.StatusCode)
	}
	return nil
}

func splitHostPort(addr string) (string, string, error) {
	idx := strings.LastIndex(addr, ":")
	if idx < 0 {
		return "", "", fmt.Errorf("missing ':' in %q", addr)
	}
	return addr[:idx], addr[idx+1:], nil
}
