// ipv6-prefix-sync keeps ConfigMap flux-system/cluster-ipv6-prefix (MLB_V6_PREFIX)
// set to the MetalLB /64: the last /64 of the delegated /60 seen on this node,
// matching the router script v6-metallb-list. On a change it asks Flux to
// reconcile the listed Kustomizations. Stdlib only; talks to the API server
// with the pod's service account.
package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// saDir is a var so tests can point it at a temp dir.
var saDir = "/var/run/secrets/kubernetes.io/serviceaccount"

const (
	cmNS   = "flux-system"
	cmName = "cluster-ipv6-prefix"
	cmKey  = "MLB_V6_PREFIX"

	ifaFlagDeprecated = 0x20
	ifaFlagTentative  = 0x40
)

// prefix60 returns the first 15 hex digits (60 bits) of the delegated prefix,
// taken from this host's global-scope, non-ULA, non-deprecated /64 addresses.
func prefix60(r io.Reader) (string, error) {
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 6 || len(f[0]) != 32 {
			continue
		}
		addr, plen, scope, flags, ifname := f[0], f[2], f[3], f[4], f[5]
		pl, _ := strconv.ParseUint(plen, 16, 8)
		fl, _ := strconv.ParseUint(flags, 16, 32)
		switch {
		case scope != "00", pl != 64: // global /64 only
		case addr[0] != '2' && addr[0] != '3': // 2000::/3 only (drops ULA fc00::/7)
		case fl&(ifaFlagDeprecated|ifaFlagTentative) != 0:
		default:
			seen[addr[:15]] = true
			log.Printf("candidate %s on %s", addr, ifname)
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	if len(seen) != 1 {
		keys := make([]string, 0, len(seen))
		for k := range seen {
			keys = append(keys, k)
		}
		return "", fmt.Errorf("expected one delegated /60, found %d: %v", len(seen), keys)
	}
	for k := range seen {
		return k, nil
	}
	return "", errors.New("unreachable")
}

// metallb64 turns 15 hex digits into the compressed first four hextets of the
// MetalLB /64 (last nibble f), e.g. 260100c20a0074c -> 2601:c2:a00:74cf.
func metallb64(p60 string) (string, error) {
	h := p60 + "f"
	parts := make([]string, 4)
	for i := range parts {
		v, err := strconv.ParseUint(h[i*4:i*4+4], 16, 16)
		if err != nil {
			return "", err
		}
		parts[i] = strconv.FormatUint(v, 16)
	}
	s := strings.Join(parts, ":")
	if net.ParseIP(s+"::") == nil {
		return "", fmt.Errorf("bad prefix %q", s)
	}
	return s, nil
}

type kube struct {
	base  string
	token string
	hc    *http.Client
}

func newKube() (*kube, error) {
	tok, err := os.ReadFile(saDir + "/token")
	if err != nil {
		return nil, err
	}
	ca, err := os.ReadFile(saDir + "/ca.crt")
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("no certs in ca.crt")
	}
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, errors.New("KUBERNETES_SERVICE_HOST/PORT not set")
	}
	return &kube{
		base:  "https://" + net.JoinHostPort(host, port),
		token: strings.TrimSpace(string(tok)),
		hc: &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		}},
	}, nil
}

func (k *kube) do(method, path, ctype string, body, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, k.base+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+k.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := k.hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("%s %s: HTTP %d: %.300s", method, path, resp.StatusCode, data)
	}
	if out != nil {
		return resp.StatusCode, json.Unmarshal(data, out)
	}
	return resp.StatusCode, nil
}

func run() error {
	src := os.Getenv("IF_INET6_PATH")
	if src == "" {
		src = "/proc/net/if_inet6"
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	p, err := prefix60(f)
	f.Close()
	if err != nil {
		return err
	}
	want, err := metallb64(p)
	if err != nil {
		return err
	}
	log.Printf("MetalLB IPv6 /64 should be %s::/64", want)
	dry := strings.EqualFold(os.Getenv("DRY_RUN"), "true")

	k, err := newKube()
	if err != nil {
		return err
	}
	cmPath := "/api/v1/namespaces/" + cmNS + "/configmaps/" + cmName
	var cm struct {
		Data map[string]string `json:"data"`
	}
	st, err := k.do("GET", cmPath, "", nil, &cm)
	exists := true
	switch {
	case st == http.StatusNotFound:
		exists = false
	case err != nil:
		return fmt.Errorf("reading ConfigMap: %w", err)
	}
	have := cm.Data[cmKey]

	if exists && have == want {
		log.Printf("no change (%s=%s)", cmKey, have)
	} else {
		log.Printf("change %s: %q -> %q (dry run: %v)", cmKey, have, want, dry)
		if !dry {
			if exists {
				_, err = k.do("PATCH", cmPath, "application/merge-patch+json",
					map[string]any{"data": map[string]string{cmKey: want}}, nil)
			} else {
				_, err = k.do("POST", "/api/v1/namespaces/"+cmNS+"/configmaps", "application/json",
					map[string]any{
						"apiVersion": "v1", "kind": "ConfigMap",
						"metadata": map[string]any{"name": cmName, "namespace": cmNS,
							"labels": map[string]string{"app.kubernetes.io/managed-by": "ipv6-prefix-sync"}},
						"data": map[string]string{cmKey: want},
					}, nil)
			}
			if err != nil {
				return fmt.Errorf("writing ConfigMap: %w", err)
			}
			stamp := time.Now().UTC().Format(time.RFC3339Nano)
			for _, ks := range strings.Split(envOr("FLUX_KUSTOMIZATIONS", "network-system,default"), ",") {
				if ks = strings.TrimSpace(ks); ks == "" {
					continue
				}
				_, err := k.do("PATCH", "/apis/kustomize.toolkit.fluxcd.io/v1/namespaces/flux-system/kustomizations/"+ks,
					"application/merge-patch+json",
					map[string]any{"metadata": map[string]any{"annotations": map[string]string{
						"reconcile.fluxcd.io/requestedAt": stamp}}}, nil)
				if err != nil {
					log.Printf("WARN reconcile request for %s: %v", ks, err)
				} else {
					log.Printf("requested reconcile of Kustomization %s", ks)
				}
			}
		}
	}

	// Informational: show whether MetalLB has the current ranges yet.
	var pools struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Addresses []string `json:"addresses"`
			} `json:"spec"`
		} `json:"items"`
	}
	if _, err := k.do("GET", "/apis/metallb.io/v1beta1/namespaces/network-system/ipaddresspools", "", nil, &pools); err == nil {
		for _, p := range pools.Items {
			var v6 []string
			ok := true
			for _, a := range p.Spec.Addresses {
				if strings.Contains(a, ":") {
					v6 = append(v6, a)
					ok = ok && strings.HasPrefix(a, want+"::")
				}
			}
			state := "ok"
			if !ok {
				state = "waiting for Flux"
			}
			log.Printf("pool %s IPv6 %v %s", p.Metadata.Name, v6, state)
		}
	}
	return nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	log.SetFlags(log.LUTC | log.Ldate | log.Ltime)
	if err := run(); err != nil {
		log.Printf("ERROR: %v", err)
		os.Exit(1)
	}
}
