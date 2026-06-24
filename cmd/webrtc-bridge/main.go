package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vakabus/kodi-screenshare/internal/kodi"
	"github.com/vakabus/kodi-screenshare/internal/mediamtx"
	"github.com/vakabus/kodi-screenshare/internal/metrics"
	"github.com/vakabus/kodi-screenshare/internal/server"
	"github.com/vakabus/kodi-screenshare/internal/session"
)

const (
	defaultHTTPListenAddr  = ":443"
	defaultMediaAPIBaseURL = "http://127.0.0.1:9997"
	defaultKodiAPIEndpoint = "127.0.0.1:9090"
	defaultStreamHost      = "127.0.0.1"
	defaultWhipBaseURL     = "http://127.0.0.1:8889"
	defaultStrmPath        = "/storage/.kodi/userdata/kodi-screenshare.strm"
	rtspStreamPort         = "8554"
	pathName               = "screenshare"
)

func main() {
	var listenAddr string
	var hookBaseURL string
	var kodiEndpoint string
	var streamHost string
	var tlsCert string
	var tlsKey string
	var mediamtxPath string
	var strmPath string

	flag.StringVar(&listenAddr, "listen-addr", defaultHTTPListenAddr, "HTTPS listen address for the web UI and API")
	flag.StringVar(&hookBaseURL, "hook-base-url", "", "Base URL for MediaMTX hooks to call back into, e.g. https://127.0.0.1:443")
	flag.StringVar(&kodiEndpoint, "kodi-endpoint", defaultKodiAPIEndpoint, "Kodi JSON-RPC TCP endpoint (host:port)")
	flag.StringVar(&streamHost, "stream-host", defaultStreamHost, "Host or IP address Kodi should use for the RTSP stream")
	flag.StringVar(&tlsCert, "tls-cert", "", "Path to TLS certificate file (auto-generated if empty)")
	flag.StringVar(&tlsKey, "tls-key", "", "Path to TLS private key file (auto-generated if empty)")
	flag.StringVar(&mediamtxPath, "mediamtx-path", "mediamtx", "Path to the MediaMTX binary")
	flag.StringVar(&strmPath, "strm-path", defaultStrmPath, "Path to write the Kodi .strm playback file (must be readable by Kodi)")
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
	streamURL := buildRTSPStreamURL(streamHost)

	// Kodi opens a .strm file (not the raw RTSP URL) so the realtime/low-latency
	// inputstream.ffmpegdirect KODIPROPs take effect. If we can't write the file
	// (e.g. during LAN dev where Kodi runs on another host), fall back to opening
	// the RTSP URL directly so the bridge still works, just without the hints.
	openTarget := strmPath
	if err := kodi.WriteStrmFile(strmPath, streamURL); err != nil {
		logger.Printf("could not write Kodi .strm file (%v); falling back to direct RTSP URL", err)
		openTarget = streamURL
	}
	kodiClient := kodi.NewClient(kodiEndpoint, openTarget)
	mediaClient := mediamtx.NewAPIClient(defaultMediaAPIBaseURL, pathName, httpClient)
	latencyMonitor := metrics.New(kodiClient.GetActivePlayerPosition, logger)
	app := server.New(state, kodiClient, mediaClient, latencyMonitor, defaultWhipBaseURL)
	manager := mediamtx.NewManager(mediamtxPath, hookBaseURL, logger)

	go func() {
		if err := manager.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Printf("MediaMTX manager stopped: %v", err)
		}
	}()

	var tlsConfig *tls.Config
	if tlsCert == "" || tlsKey == "" {
		cert, err := generateSelfSignedCert()
		if err != nil {
			logger.Fatalf("generate self-signed certificate: %v", err)
		}
		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
		logger.Printf("using auto-generated self-signed TLS certificate")
	}

	httpServer := &http.Server{
		Addr:              listenAddr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig:         tlsConfig,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	logger.Printf("serving web UI and API on %s (HTTPS)", listenAddr)
	logger.Printf("Kodi will open %s (RTSP %s)", openTarget, streamURL)
	if err := httpServer.ListenAndServeTLS(tlsCert, tlsKey); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatalf("HTTPS server stopped: %v", err)
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

	return "https://" + net.JoinHostPort(host, port), nil
}

func generateSelfSignedCert() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate ECDSA key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate serial number: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      pkix.Name{CommonName: "kodi-screenshare"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		DNSNames:     []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return tls.X509KeyPair(certPEM, keyPEM)
}

func buildRTSPStreamURL(host string) string {
	return (&url.URL{
		Scheme: "rtsp",
		Host:   net.JoinHostPort(host, rtspStreamPort),
		Path:   "/" + pathName,
	}).String()
}
