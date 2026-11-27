package main

import (
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestRunAgainstFakeAPI drives run() against a fake API server: first run
// creates the ConfigMap and requests reconciles, second run is a no-op.
func TestRunAgainstFakeAPI(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	cm := map[string]string(nil)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/configmaps/cluster-ipv6-prefix") && r.Method == "GET":
			if cm == nil {
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"data": cm})
		case strings.HasSuffix(r.URL.Path, "/configmaps") && r.Method == "POST":
			var o struct{ Data map[string]string }
			json.Unmarshal(body, &o)
			cm = o.Data
			w.WriteHeader(201)
		case strings.Contains(r.URL.Path, "/kustomizations/") && r.Method == "PATCH":
			if r.Header.Get("Content-Type") != "application/merge-patch+json" {
				w.WriteHeader(415)
			}
		case strings.HasSuffix(r.URL.Path, "/ipaddresspools"):
			w.Write([]byte(`{"items":[{"metadata":{"name":"system"},"spec":{"addresses":["10.0.100.0/24","2601:c2:a00:74cf::100:0/112"]}}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	saDir = dir
	os.WriteFile(filepath.Join(dir, "token"), []byte("tok\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "ca.crt"),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600)
	ifp := filepath.Join(dir, "if_inet6")
	os.WriteFile(ifp, []byte(sample), 0o600)
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "https://"))
	t.Setenv("KUBERNETES_SERVICE_HOST", host)
	t.Setenv("KUBERNETES_SERVICE_PORT", port)
	t.Setenv("IF_INET6_PATH", ifp)

	if err := run(); err != nil {
		t.Fatal(err)
	}
	if cm[cmKey] != "2601:c2:a00:74cf" {
		t.Fatalf("ConfigMap = %v", cm)
	}
	patches := 0
	for _, c := range calls {
		if strings.HasPrefix(c, "PATCH ") && strings.Contains(c, "/kustomizations/") {
			patches++
		}
	}
	if patches != 2 {
		t.Fatalf("want 2 reconcile patches, got %d: %v", patches, calls)
	}
	calls = nil
	if err := run(); err != nil {
		t.Fatal(err)
	}
	for _, c := range calls {
		if !strings.HasPrefix(c, "GET ") {
			t.Fatalf("second run should only read, got %v", calls)
		}
	}
}
