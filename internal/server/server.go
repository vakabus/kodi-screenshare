package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/vakabus/kodi-screenshare/internal/metrics"
	webui "github.com/vakabus/kodi-screenshare/web"
)

type SessionState interface {
	IsActive() bool
	SetActive(bool)
}

type KodiController interface {
	Open(context.Context) error
	Stop(context.Context) error
}

type MediaController interface {
	KickActivePublisher(context.Context) (bool, error)
}

// LatencyMonitor collects the playback latency time series while a share is active.
type LatencyMonitor interface {
	Start()
	Stop()
	Snapshot() (active bool, current float64, samples []metrics.Sample)
}

type Server struct {
	session     SessionState
	kodi        KodiController
	media       MediaController
	metrics     LatencyMonitor
	whipBaseURL string
}

func New(session SessionState, kodi KodiController, media MediaController, metrics LatencyMonitor, whipBaseURL string) *Server {
	return &Server{
		session:     session,
		kodi:        kodi,
		media:       media,
		metrics:     metrics,
		whipBaseURL: whipBaseURL,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/metrics", s.handleMetrics)
	mux.HandleFunc("/api/takeover", s.handleTakeover)
	mux.HandleFunc("/api/hooks/ready", s.handleReady)
	mux.HandleFunc("/api/hooks/not-ready", s.handleNotReady)
	if s.whipBaseURL != "" {
		mux.Handle("/screenshare/", s.whipReverseProxy())
	}
	mux.Handle("/", http.FileServer(http.FS(webui.Assets)))
	return mux
}

func (s *Server) whipReverseProxy() http.Handler {
	target, err := url.Parse(s.whipBaseURL)
	if err != nil {
		log.Fatalf("parse WHIP base URL %q: %v", s.whipBaseURL, err)
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
		},
		ModifyResponse: func(resp *http.Response) error {
			loc := resp.Header.Get("Location")
			if loc == "" {
				return nil
			}
			parsed, err := url.Parse(loc)
			if err != nil {
				return nil
			}
			if parsed.IsAbs() && strings.HasPrefix(parsed.Path, "/screenshare/") {
				parsed.Scheme = ""
				parsed.Host = ""
				resp.Header.Set("Location", parsed.String())
			}
			return nil
		},
	}
	return proxy
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"active": s.session.IsActive()})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	active, current, samples := s.metrics.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"active":  active,
		"current": current,
		"samples": samples,
	})
}

func (s *Server) handleTakeover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	log.Printf("takeover requested")

	kicked, err := s.media.KickActivePublisher(r.Context())
	if err != nil {
		log.Printf("takeover failed: %v", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if kicked {
		s.session.SetActive(false)
	}
	log.Printf("takeover finished; kicked=%t", kicked)

	writeJSON(w, http.StatusOK, map[string]bool{"kicked": kicked})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	log.Printf("MediaMTX ready hook received")
	s.session.SetActive(true)
	log.Printf("invoking Kodi.Open")
	if err := s.kodi.Open(r.Context()); err != nil {
		log.Printf("Kodi.Open failed: %v", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	log.Printf("Kodi.Open succeeded")
	s.metrics.Start()

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleNotReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	log.Printf("MediaMTX not-ready hook received")
	s.session.SetActive(false)
	s.metrics.Stop()
	log.Printf("invoking Kodi.Stop")
	if err := s.kodi.Stop(r.Context()); err != nil {
		log.Printf("Kodi.Stop failed: %v", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	log.Printf("Kodi.Stop succeeded")

	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
