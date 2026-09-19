package proxy_test

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andybalholm/brotli"

	"github.com/siner308/sims/internal/proxy"
)

// start runs a proxy against a throwaway CA and returns a client that goes through it and trusts it.
// configure runs before the server listens, which is when its fields may still be set.
func start(t *testing.T, configure ...func(*proxy.Server)) (*proxy.Server, *http.Client) {
	t.Helper()
	ca, err := proxy.LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := &proxy.Server{CA: ca, Store: proxy.NewStore(0)}
	for _, fn := range configure {
		fn(srv)
	}
	port, err := srv.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		if err := srv.Serve(); err != nil {
			t.Error(err)
		}
	}()
	t.Cleanup(func() { srv.Close() })

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca.CertPEM()) {
		t.Fatal("ca.pem did not parse as a root")
	}
	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}
	return srv, client
}

func waitFlows(t *testing.T, s *proxy.Store, n int) []proxy.Flow {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		flows := s.Flows()
		done := 0
		for _, f := range flows {
			if f.Done {
				done++
			}
		}
		if done >= n {
			return flows
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("only %d flows after 5s, wanted %d", s.Len(), n)
	return nil
}

func TestPlainHTTP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"saw":%q}`, string(body))
	}))
	defer upstream.Close()

	srv, client := start(t)
	resp, err := client.Post(upstream.URL+"/echo?x=1", "text/plain", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(got), "hello") {
		t.Fatalf("body = %q", got)
	}

	flows := waitFlows(t, srv.Store, 1)
	f := flows[0]
	if f.Method != "POST" || f.Status != 200 {
		t.Errorf("flow = %s %d", f.Method, f.Status)
	}
	if !strings.HasSuffix(f.URL, "/echo?x=1") {
		t.Errorf("url = %q", f.URL)
	}
	if string(f.ReqBody) != "hello" {
		t.Errorf("request body = %q", f.ReqBody)
	}
	if !strings.Contains(string(f.RespBody), "hello") {
		t.Errorf("response body = %q", f.RespBody)
	}
	if f.Kind != proxy.KindHTTP {
		t.Errorf("kind = %q", f.Kind)
	}
}

func TestHTTPSIsDecrypted(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"path":%q}`, r.URL.Path)
	}))
	defer upstream.Close()

	// the upstream's own certificate is self-signed, so the proxy must be told to accept it
	srv, client := start(t, trustAnything)

	resp, err := client.Get(upstream.URL + "/secret")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "/secret") {
		t.Fatalf("body = %q", body)
	}

	flows := waitFlows(t, srv.Store, 1)
	var https *proxy.Flow
	for i := range flows {
		if flows[i].Kind == proxy.KindHTTP {
			https = &flows[i]
		}
	}
	if https == nil {
		t.Fatalf("no decrypted flow: %+v", flows)
	}
	if !strings.HasPrefix(https.URL, "https://") || https.Path != "/secret" {
		t.Errorf("url = %q path = %q", https.URL, https.Path)
	}
	if !strings.Contains(string(https.RespBody), "/secret") {
		t.Errorf("response body = %q", https.RespBody)
	}
}

func TestUntrustingClientIsTunnelledAndExplained(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()

	srv, _ := start(t, trustAnything)
	proxyURL, _ := url.Parse("http://" + srv.Addr().String())
	// a client that does not trust the sims CA: the handshake fails, which is what a pinned app looks like
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	if _, err := client.Get(upstream.URL + "/"); err == nil {
		t.Fatal("expected the handshake to fail")
	}
	flows := waitFlows(t, srv.Store, 1)
	f := flows[len(flows)-1]
	if f.Kind != proxy.KindTunnel {
		t.Fatalf("kind = %q", f.Kind)
	}
	if !strings.Contains(f.Error, "does not trust") {
		t.Errorf("error = %q", f.Error)
	}
}

// The proxy hands a compressed body on untouched; only the flow detail decodes it, for reading.
// The wire bytes are checked in TestRawGzipThroughProxy, where no client can decode behind our back.
func TestGzipBodyReadsBackDecoded(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(gzipped(t, `{"a":1}`))
	}))
	defer upstream.Close()

	srv, client := start(t)
	resp, err := client.Get(upstream.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	f := waitFlows(t, srv.Store, 1)[0]
	if f.RespHeader.Get("Content-Encoding") != "gzip" {
		t.Errorf("the flow lost Content-Encoding: %q", f.RespHeader.Get("Content-Encoding"))
	}
	if pretty := proxy.Pretty(f.RespHeader, f.RespBody); !strings.Contains(pretty, `"a": 1`) {
		t.Errorf("pretty = %q", pretty)
	}
}

func TestHARRoundTrips(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "hi")
	}))
	defer upstream.Close()
	srv, client := start(t)
	resp, err := client.Get(upstream.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	waitFlows(t, srv.Store, 1)

	var buf bytes.Buffer
	if err := proxy.WriteHAR(&buf, "test", srv.Store.Flows()); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Log struct {
			Entries []struct {
				Request  struct{ URL, Method string }
				Response struct {
					Status  int
					Content struct{ Text string }
				}
			}
		}
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Log.Entries) != 1 {
		t.Fatalf("entries = %d", len(doc.Log.Entries))
	}
	e := doc.Log.Entries[0]
	if e.Request.Method != "GET" || e.Response.Status != 200 || e.Response.Content.Text != "hi" {
		t.Errorf("entry = %+v", e)
	}
}

func TestCAIsReusedAcrossRuns(t *testing.T) {
	dir := t.TempDir()
	first, err := proxy.LoadOrCreateCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := proxy.LoadOrCreateCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint() != second.Fingerprint() {
		t.Error("a second load made a new CA instead of reading the stored one")
	}
	if !bytes.Equal(first.CertPEM(), second.CertPEM()) {
		t.Error("ca.pem differs between loads")
	}
	leaf, err := second.Leaf("example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := leaf.Leaf.VerifyHostname("example.com"); err != nil {
		t.Error(err)
	}
	// Apple refuses a server certificate valid for more than 398 days
	if days := time.Until(leaf.Leaf.NotAfter).Hours() / 24; days > 398 {
		t.Errorf("leaf is valid for %.0f days; iOS refuses more than 398", days)
	}
}

func gzipped(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := io.WriteString(zw, s); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func trustAnything(s *proxy.Server) {
	s.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
}

// The attribution in the flows table depends on this working for a live connection; if it cannot
// name the process, every row says "-" and the device filter has nothing to separate.
func TestLookupProcessNamesThisTest(t *testing.T) {
	var seen string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		p, err := proxy.LookupProcess(t.Context(), c.RemoteAddr().String())
		if err != nil {
			t.Logf("LookupProcess(%s) failed: %v", c.RemoteAddr(), err)
			return
		}
		seen = p.Name
		t.Logf("LookupProcess(%s) = pid=%d name=%q path=%q", c.RemoteAddr(), p.PID, p.Name, p.Path)
		io.WriteString(c, "hi")
	}()

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(c)
	c.Close()
	<-done
	if seen == "" {
		t.Error("could not name the process behind a live loopback connection")
	}
}

// A leaf has to satisfy the platform's own verifier, not just parse. Apple refuses a server
// certificate that is missing an EKU, has no SAN, or runs longer than 398 days.
func TestLeafVerifiesAgainstTheRoot(t *testing.T) {
	ca, err := proxy.LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca.CertPEM()) {
		t.Fatal("root did not parse")
	}
	for _, host := range []string{"www.apple.com", "example.com", "192.168.1.5"} {
		leaf, err := ca.Leaf(host)
		if err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		chains, err := leaf.Leaf.Verify(x509.VerifyOptions{
			Roots:       roots,
			DNSName:     dnsNameFor(host),
			CurrentTime: time.Now(),
			KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		})
		if err != nil {
			t.Errorf("%s: leaf does not verify against its own root: %v", host, err)
			continue
		}
		if len(chains) == 0 {
			t.Errorf("%s: no chain", host)
		}
		if got := leaf.Leaf.NotAfter.Sub(leaf.Leaf.NotBefore); got > 398*24*time.Hour {
			t.Errorf("%s: validity %v exceeds Apple's 398-day limit", host, got)
		}
	}
}

// The chain the proxy presents must include the root, or a client that trusts the root by its own
// copy still has to build the chain itself.
func TestLeafChainIncludesRoot(t *testing.T) {
	ca, err := proxy.LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := ca.Leaf("example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(leaf.Certificate) != 2 {
		t.Fatalf("the presented chain has %d certificates, want leaf plus root", len(leaf.Certificate))
	}
	var cfg tls.Certificate = *leaf
	if cfg.Leaf == nil {
		t.Error("Leaf is not parsed, so the TLS stack re-parses it on every handshake")
	}
}

func dnsNameFor(host string) string {
	if host == "192.168.1.5" {
		return ""
	}
	return host
}

func TestRawGzipThroughProxy(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("upstream Accept-Encoding: %q", r.Header.Get("Accept-Encoding"))
		w.Header().Set("Content-Encoding", "gzip")
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		io.WriteString(zw, `{"a":1}`)
		zw.Close()
		w.Write(buf.Bytes())
	}))
	defer up.Close()

	ca, err := proxy.LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := &proxy.Server{CA: ca, Store: proxy.NewStore(0)}
	port, err := srv.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve()
	defer srv.Close()

	c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	u, _ := url.Parse(up.URL)
	fmt.Fprintf(c, "GET %s/ HTTP/1.1\r\nHost: %s\r\nAccept-Encoding: gzip\r\nConnection: close\r\n\r\n", up.URL, u.Host)
	raw, _ := io.ReadAll(c)
	i := bytes.Index(raw, []byte("\r\n\r\n"))
	head, body := raw[:i], raw[i+4:]
	t.Logf("--- head ---\n%s", head)
	t.Logf("body len=%d gzipmagic=%v first=%q", len(body), len(body) > 1 && body[0] == 0x1f, string(body[:min(len(body), 20)]))
}

// A request inside a CONNECT tunnel outlives the CONNECT handler. When the inner server borrowed the
// CONNECT's context, anything slower than the handler's return was cancelled mid-flight.
func TestSlowRequestInsideTunnelCompletes(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(400 * time.Millisecond)
		fmt.Fprint(w, "finished")
	}))
	defer upstream.Close()

	ca, err := proxy.LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := &proxy.Server{
		CA:        ca,
		Store:     proxy.NewStore(0),
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	port, err := srv.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve()
	defer srv.Close()

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.CertPEM())
	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{RootCAs: pool},
	}}

	resp, err := client.Get(upstream.URL + "/slow")
	if err != nil {
		t.Fatalf("a slow request through the tunnel failed: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("reading the body failed: %v", err)
	}
	if string(body) != "finished" {
		t.Errorf("body = %q", body)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, f := range srv.Store.Flows() {
			if f.Kind == proxy.KindHTTP && f.Done {
				if f.Error != "" {
					t.Errorf("flow recorded an error: %q", f.Error)
				}
				if f.Status != 200 {
					t.Errorf("status = %d", f.Status)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("no completed HTTP flow was recorded")
}

// The proxy verifies the upstream's certificate the way the device would have. Accepting a bad one
// would turn a debugging tool into a way to hide a real attack from the device it is watching.
func TestUpstreamCertificateIsVerified(t *testing.T) {
	// httptest's TLS server signs with a certificate no system root vouches for
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "should not reach the client")
	}))
	defer upstream.Close()

	srv, client := start(t)
	resp, err := client.Get(upstream.URL + "/")
	if err != nil {
		return // refusing outright is also correct
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 for an upstream sims cannot verify", resp.StatusCode)
	}
	if strings.Contains(string(body), "should not reach") {
		t.Error("the proxy passed an unverified upstream's body to the client")
	}

	flows := waitFlows(t, srv.Store, 1)
	last := flows[len(flows)-1]
	if last.Error == "" {
		t.Error("the flow does not record why the upstream was refused")
	}
}

// A response the proxy could not finish relaying must reach the client as a broken connection. A
// truncated body under a 200 would have the app under test see something that never happened.
func TestTruncatedResponseBreaksTheConnection(t *testing.T) {
	// the handler promises more than it sends, then drops the connection
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("only a few bytes"))
		http.NewResponseController(w).Flush()
		panic(http.ErrAbortHandler)
	}))
	defer upstream.Close()

	_, client := start(t)
	resp, err := client.Get(upstream.URL + "/short")
	if err != nil {
		return // refused outright, which is also a broken connection
	}
	_, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr == nil {
		t.Error("the client read a truncated body as if the response were complete")
	}
}

// Stopping a capture has to stop the relaying too. net/http's Shutdown does not touch hijacked
// connections, and every CONNECT is hijacked, so a tunnel outlived the session that owned it.
func TestCloseEndsAnOpenTunnel(t *testing.T) {
	// an upstream that holds the connection open without answering
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()

	ca, err := proxy.LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := &proxy.Server{CA: ca, Store: proxy.NewStore(0)}
	port, err := srv.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve()

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", ln.Addr(), ln.Addr())
	buf := make([]byte, 64)
	if _, err := conn.Read(buf); err != nil {
		t.Fatalf("no CONNECT response: %v", err)
	}

	srv.Close()

	// the tunnel must now be gone rather than still relaying
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Read(buf); err == nil {
		t.Error("the tunnel was still open after the capture closed")
	} else if strings.Contains(err.Error(), "i/o timeout") {
		t.Error("the tunnel outlived Close: it is still holding the connection")
	}
}

// The CA key is the whole trust of this feature: anyone holding it can impersonate any site to every
// device that trusts it. A key others can read is refused rather than used.
func TestWorldReadableKeyIsRefused(t *testing.T) {
	dir := t.TempDir()
	if _, err := proxy.LoadOrCreateCA(dir); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "ca-key.pem")
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := proxy.LoadOrCreateCA(dir)
	if err == nil {
		t.Fatal("a key other users can read was loaded anyway")
	}
	if !strings.Contains(err.Error(), "readable by other users") {
		t.Errorf("error = %q", err)
	}
}

func TestFreshKeyIsPrivate(t *testing.T) {
	dir := t.TempDir()
	if _, err := proxy.LoadOrCreateCA(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "ca-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("a new key is written %04o", mode)
	}
}

// Most HTTPS sites answer in brotli now. Without a decoder every one of those bodies reads as
// "(binary)" in the detail, which looks like the proxy failed rather than like a missing codec.
func TestBrotliBodyIsReadable(t *testing.T) {
	var buf bytes.Buffer
	bw := brotli.NewWriter(&buf)
	if _, err := io.WriteString(bw, `{"hello":"world"}`); err != nil {
		t.Fatal(err)
	}
	if err := bw.Close(); err != nil {
		t.Fatal(err)
	}
	h := http.Header{"Content-Encoding": []string{"br"}, "Content-Type": []string{"application/json"}}
	got := proxy.Pretty(h, buf.Bytes())
	if !strings.Contains(got, `"hello": "world"`) {
		t.Errorf("a brotli body rendered as %q", got)
	}
}

// An encoding sims has no decoder for must not be mangled: the raw bytes come back untouched.
func TestUnknownEncodingIsLeftAlone(t *testing.T) {
	h := http.Header{"Content-Encoding": []string{"zstd"}}
	raw := []byte("not really zstd")
	if got := string(proxy.DecodeBody(h, raw)); got != string(raw) {
		t.Errorf("DecodeBody changed a body it cannot decode: %q", got)
	}
}
