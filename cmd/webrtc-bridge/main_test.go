package main

import "testing"

func TestDeriveHookBaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		listenAddr string
		want       string
		wantErr    bool
	}{
		{name: "port only", listenAddr: ":8081", want: "https://127.0.0.1:8081"},
		{name: "wildcard host", listenAddr: "0.0.0.0:8081", want: "https://127.0.0.1:8081"},
		{name: "specific host", listenAddr: "192.168.1.50:8081", want: "https://192.168.1.50:8081"},
		{name: "named host", listenAddr: "localhost:8081", want: "https://localhost:8081"},
		{name: "invalid address", listenAddr: "8081", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := deriveHookBaseURL(tt.listenAddr)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("deriveHookBaseURL(%q) expected error", tt.listenAddr)
				}
				return
			}
			if err != nil {
				t.Fatalf("deriveHookBaseURL(%q) error = %v", tt.listenAddr, err)
			}
			if got != tt.want {
				t.Fatalf("deriveHookBaseURL(%q) = %q, want %q", tt.listenAddr, got, tt.want)
			}
		})
	}
}

func TestBuildRTSPStreamURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		host string
		want string
	}{
		{name: "ipv4", host: "192.168.1.50", want: "rtsp://192.168.1.50:8554/screenshare"},
		{name: "hostname", host: "bridge.local", want: "rtsp://bridge.local:8554/screenshare"},
		{name: "ipv6", host: "2001:db8::10", want: "rtsp://[2001:db8::10]:8554/screenshare"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := buildRTSPStreamURL(tt.host); got != tt.want {
				t.Fatalf("buildRTSPStreamURL(%q) = %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}
