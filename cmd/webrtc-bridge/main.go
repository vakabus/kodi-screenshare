package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/vakabus/kodi-screenshare/internal/kodi"
	"github.com/vakabus/kodi-screenshare/internal/mediamtx"
	"github.com/vakabus/kodi-screenshare/internal/server"
	"github.com/vakabus/kodi-screenshare/internal/session"
)

const (
	defaultHTTPListenAddr  = ":80"
	defaultMediaAPIBaseURL = "http://127.0.0.1:9997"
	defaultKodiAPIEndpoint = "http://127.0.0.1:8080/jsonrpc"
	defaultStreamHost      = "127.0.0.1"
	hlsStreamPort          = "8888"
	pathName               = "screenshare"
)

func main() {
	var listenAddr string
	var hookBaseURL string
	var kodiEndpoint string
	var kodiUsername string
	var kodiPassword string
	var streamHost string

	flag.StringVar(&listenAddr, "listen-addr", defaultHTTPListenAddr, "HTTP listen address for the web UI and API")
	flag.StringVar(&hookBaseURL, "hook-base-url", "", "Base URL for MediaMTX hooks to call back into, e.g. http://127.0.0.1:8081")
	flag.StringVar(&kodiEndpoint, "kodi-endpoint", defaultKodiAPIEndpoint, "Kodi JSON-RPC endpoint")
	flag.StringVar(&kodiUsername, "kodi-username", "", "Kodi web server username for HTTP basic auth")
	flag.StringVar(&kodiPassword, "kodi-password", "", "Kodi web server password for HTTP basic auth")
	flag.StringVar(&streamHost, "stream-host", defaultStreamHost, "Host or IP address Kodi should use for the HLS stream")
	flag.Parse()

	logger := log.New(os.Stdout, "webrtc-bridge: ", log.LstdFlags|log.Lmsgprefix)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if hookBaseURL == "" {
		derivedBaseURL, err := deriveHookBaseURL(listenAddr)
		if err != nil {
			logger.Fatalf("configure MediaMTX hooks: %v", err)
		}
		hookBaseURL = derivedBaseURL
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}
	state := session.NewState()
	streamURL := buildHLSStreamURL(streamHost)
	readinessURL := buildHLSStreamURL(defaultStreamHost)
	kodiClient := kodi.NewClient(kodiEndpoint, streamURL, readinessURL, kodiUsername, kodiPassword, httpClient)
	mediaClient := mediamtx.NewAPIClient(defaultMediaAPIBaseURL, pathName, httpClient)
	app := server.New(state, kodiClient, mediaClient)
	manager := mediamtx.NewManager(filepath.Join(".", "third_party", "mediamtx", "mediamtx"), hookBaseURL, logger)

	go func() {
		if err := manager.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Printf("MediaMTX manager stopped: %v", err)
		}
	}()

	httpServer := &http.Server{
		Addr:              listenAddr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	logger.Printf("serving web UI and API on %s", listenAddr)
	logger.Printf("Kodi will open HLS stream at %s", streamURL)
	logger.Printf("bridge will wait for local HLS readiness at %s", readinessURL)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatalf("HTTP server stopped: %v", err)
	}
}

func deriveHookBaseURL(listenAddr string) (string, error) {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "", fmt.Errorf("parse listen address %q: %w", listenAddr, err)
	}

	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}

	return "http://" + net.JoinHostPort(host, port), nil
}

func buildHLSStreamURL(host string) string {
	return (&url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(host, hlsStreamPort),
		Path:   "/" + pathName + "/index.m3u8",
	}).String()
}
