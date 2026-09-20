package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// AttributeBudget bounds how long a connection waits for Attribute to say who owns it. The lookup
// runs before the first request, so a slow answer is latency the captured app feels; past the
// budget the connection is treated as unattributed, which under the default scope means it is
// relayed untouched rather than opened on a guess.
const AttributeBudget = 400 * time.Millisecond

const (
	// DefaultMaxBody is what is kept of each body for reading later. It is generous because the
	// point of a capture is to look at what went past, and a response too big to keep is exactly
	// the one worth having: an image, a video or a bundle. The store's own ceiling still bounds
	// total memory, and nothing here limits what reaches the client either way.
	DefaultMaxBody   = 100 << 20
	handshakeTimeout = 15 * time.Second
	// peekTimeout bounds how long a CONNECT waits for the client's first byte before it is tunnelled
	// as is: a client that expects the server to speak first would otherwise hang.
	peekTimeout = 10 * time.Second
)

// Attribution is what the owner of the proxy knows about a client connection: a label for the table,
// whether it came from the device the proxy was started for, and whether sims should open it at all.
type Attribution struct {
	Label  string
	Origin Origin
	// Ignore passes the connection straight through: its bytes are relayed untouched and nothing is
	// recorded. A capture that has to borrow this machine's proxy settings uses it to leave every
	// other app on the machine exactly as it was.
	Ignore bool
}

// Server is one listening proxy. Every field must be set before Listen, which freezes them.
type Server struct {
	CA    *CA
	Store *Store
	// MaxBody caps how much of each body is kept; the rest still flows through. Zero means DefaultMaxBody.
	MaxBody int
	// Attribute is asked once per client connection, before the first request is read, because
	// whether a connection is ours to open has to be decided before anything is read from it. It is
	// given AttributeBudget to answer; nil leaves flows unattributed.
	Attribute func(ctx context.Context, clientAddr string) Attribution
	// Transport reaches the upstream; nil builds one that ignores this machine's proxy settings.
	Transport http.RoundTripper

	ln   net.Listener
	srv  *http.Server
	once sync.Once

	// hijacked holds the CONNECT connections, which Shutdown does not know about: a tunnel would
	// otherwise keep relaying after the capture reported itself stopped.
	hijackedMu sync.Mutex
	hijacked   map[net.Conn]struct{}
	closed     bool
}

// track registers a hijacked connection and reports whether the server is still open. A connection
// hijacked during a Close is closed at once rather than left relaying.
func (s *Server) track(c net.Conn) bool {
	s.hijackedMu.Lock()
	defer s.hijackedMu.Unlock()
	if s.closed {
		return false
	}
	if s.hijacked == nil {
		s.hijacked = map[net.Conn]struct{}{}
	}
	s.hijacked[c] = struct{}{}
	return true
}

func (s *Server) untrack(c net.Conn) {
	s.hijackedMu.Lock()
	delete(s.hijacked, c)
	s.hijackedMu.Unlock()
}

func (s *Server) closeHijacked() {
	s.hijackedMu.Lock()
	conns := make([]net.Conn, 0, len(s.hijacked))
	for c := range s.hijacked {
		conns = append(conns, c)
	}
	s.hijacked = nil
	s.closed = true
	s.hijackedMu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// Listen binds addr ("127.0.0.1:0" for any free port) and returns the port it got. It also freezes
// the configuration: a field set after this call is not picked up, and racing one is a data race.
func (s *Server) Listen(addr string) (int, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return 0, err
	}
	s.ln = ln
	s.init()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// Addr is the bound address after Listen.
func (s *Server) Addr() net.Addr {
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

// Serve runs until Close; it returns nil after a Close.
func (s *Server) Serve() error {
	s.init()
	err := s.srv.Serve(s.ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) init() {
	s.once.Do(func() {
		if s.Store == nil {
			s.Store = NewStore(0)
		}
		if s.MaxBody <= 0 {
			s.MaxBody = DefaultMaxBody
		}
		if s.Transport == nil {
			s.Transport = &http.Transport{
				Proxy:                 nil,
				DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConnsPerHost:   8,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: time.Second,
				DisableCompression:    true,
			}
		}
		s.srv = &http.Server{
			Handler:     http.HandlerFunc(s.handleOuter),
			ConnContext: s.connContext,
			ErrorLog:    nil,
		}
	})
}

// Close stops listening and drops open connections.
func (s *Server) Close() error {
	if s.srv == nil {
		if s.ln != nil {
			return s.ln.Close()
		}
		return nil
	}
	// Shutdown leaves hijacked connections alone, and every CONNECT is hijacked
	s.closeHijacked()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.srv.Shutdown(ctx); err != nil {
		return s.srv.Close()
	}
	return nil
}

// connInfo rides on the context of every request from one client connection. The attribution is
// resolved once, lazily, and then reused: the lookup costs an lsof, and every request on the
// connection has the same answer.
type connInfo struct {
	client    string
	attribute func(context.Context, string) Attribution
	once      sync.Once
	mu        sync.Mutex
	attr      Attribution
	known     bool
}

type connKey struct{}

func (s *Server) connContext(ctx context.Context, c net.Conn) context.Context {
	return context.WithValue(ctx, connKey{}, &connInfo{client: c.RemoteAddr().String(), attribute: s.Attribute})
}

// resolve asks who owns this connection, once. The answer decides whether the connection is opened
// or passed through, so unlike the label it cannot be filled in later.
func (ci *connInfo) resolve(ctx context.Context) Attribution {
	ci.once.Do(func() {
		if ci.attribute == nil {
			return
		}
		// the answer decides whether this connection is opened, so it cannot be deferred; it can be
		// given a deadline, and a lookup that overruns finishes in the background for the flow's
		// own label, which finish() re-reads
		done := make(chan Attribution, 1)
		go func() {
			a := ci.attribute(context.WithoutCancel(ctx), ci.client)
			ci.mu.Lock()
			ci.attr, ci.known = a, true
			ci.mu.Unlock()
			done <- a
		}()
		select {
		case <-done:
		case <-time.After(AttributeBudget):
		}
	})
	ci.mu.Lock()
	defer ci.mu.Unlock()
	return ci.attr
}

func (ci *connInfo) attribution() (Attribution, bool) {
	ci.mu.Lock()
	defer ci.mu.Unlock()
	return ci.attr, ci.known
}

func infoFrom(ctx context.Context) *connInfo {
	if ci, ok := ctx.Value(connKey{}).(*connInfo); ok {
		return ci
	}
	return &connInfo{}
}

func (s *Server) handleOuter(w http.ResponseWriter, r *http.Request) {
	// Whether this connection is ours to read is decided before anything is read from it.
	ignore := infoFrom(r.Context()).resolve(r.Context()).Ignore
	if r.Method == http.MethodConnect {
		s.handleConnect(w, r, ignore)
		return
	}
	if ignore {
		// an upgrade cannot survive a RoundTrip: relay strips Connection and Upgrade, so the origin
		// answers 200 and the app's websocket silently never establishes. Traffic sims was not
		// asked to watch has to come out the other side unchanged.
		if isUpgrade(r) {
			s.relayUpgrade(w, r)
			return
		}
		s.relay(w, r)
		return
	}
	if !r.URL.IsAbs() {
		http.Error(w, "sims proxy: this is a proxy, not a web server", http.StatusBadRequest)
		return
	}
	s.forward(w, r, r.URL.Scheme)
}

func (s *Server) newFlow(ctx context.Context, kind Kind, method, scheme, host, path string) *liveFlow {
	ci := infoFrom(ctx)
	l := &liveFlow{f: Flow{
		Kind: kind, Start: time.Now(), Client: ci.client,
		Method: method, Host: host, Path: path, URL: scheme + "://" + host + path,
	}}
	if a, ok := ci.attribution(); ok {
		l.f.Process, l.f.Origin = a.Label, a.Origin
	}
	s.Store.add(l)
	return l
}

// finish records the end of a flow and re-reads the attribution, which lsof often delivers after the
// flow started.
func (s *Server) finish(ctx context.Context, l *liveFlow, fn func(*Flow)) {
	a, known := infoFrom(ctx).attribution()
	l.update(func(f *Flow) {
		if fn != nil {
			fn(f)
		}
		if known {
			f.Process, f.Origin = a.Label, a.Origin
		}
		f.Duration = time.Since(f.Start)
		f.Done = true
	})
	s.Store.touch(l)
}

// hopByHop are the headers that describe this connection rather than the message; RFC 7230 §6.1.
var hopByHop = []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"}

func stripHopByHop(h http.Header) {
	// Connection can appear more than once, and Get returns only the first: a header named in a
	// later line would be forwarded to the origin after the client asked for it to stop here
	for _, line := range h.Values("Connection") {
		for _, name := range strings.Split(line, ",") {
			if name = strings.TrimSpace(name); name != "" {
				h.Del(name)
			}
		}
	}
	for _, name := range hopByHop {
		h.Del(name)
	}
}

// proxyOnlyHeaders are the ones that belong to the hop between the client and this proxy. An
// upgrade keeps Connection and Upgrade, which carry the handshake, but must not carry the
// credentials the client sent to the proxy on to whatever origin it is reaching.
var proxyOnlyHeaders = []string{"Proxy-Connection", "Proxy-Authorization", "Proxy-Authenticate", "Keep-Alive"}

func stripProxyOnly(h http.Header) {
	for _, name := range proxyOnlyHeaders {
		h.Del(name)
	}
}

// forward is the request path for both a plain proxy request and one read inside a TLS tunnel.
func (s *Server) forward(w http.ResponseWriter, r *http.Request, scheme string) {
	ctx := r.Context()
	if isUpgrade(r) {
		s.websocket(w, r, scheme)
		return
	}
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	f := s.newFlow(ctx, KindHTTP, r.Method, scheme, host, r.URL.RequestURI())
	f.update(func(f *Flow) { f.ReqHeader = r.Header.Clone() })

	out := r.Clone(ctx)
	out.RequestURI = ""
	out.URL.Scheme = scheme
	out.URL.Host = host
	out.Host = host
	stripHopByHop(out.Header)
	reqCap := &capture{max: s.MaxBody}
	if r.Body != nil && r.Body != http.NoBody {
		out.Body = io.NopCloser(io.TeeReader(r.Body, reqCap))
	}

	resp, err := s.Transport.RoundTrip(out)
	if err != nil {
		s.finish(ctx, f, func(f *Flow) {
			f.Error = err.Error()
			f.ReqBody, f.ReqSize, f.ReqTruncated = reqCap.buf, reqCap.n, reqCap.truncated
		})
		http.Error(w, "sims proxy: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	stripHopByHop(resp.Header)
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	respCap := &capture{max: s.MaxBody}
	rc := http.NewResponseController(w)
	_ = rc.Flush()
	_, copyErr := io.Copy(&flushWriter{w: w, rc: rc}, io.TeeReader(resp.Body, respCap))
	s.finish(ctx, f, func(f *Flow) {
		f.Status, f.StatusText = resp.StatusCode, http.StatusText(resp.StatusCode)
		f.RespHeader = resp.Header.Clone()
		f.ReqBody, f.ReqSize, f.ReqTruncated = reqCap.buf, reqCap.n, reqCap.truncated
		f.RespBody, f.RespSize, f.RespTruncated = respCap.buf, respCap.n, respCap.truncated
		switch {
		case copyErr == nil:
		case clientHungUp(ctx, copyErr):
			f.Abandoned = true
		default:
			f.Error = "response cut short: " + copyErr.Error()
		}
	})
	if copyErr != nil {
		// The status and Content-Length are already on their way to the client, so returning here
		// would hand it a short body that still looks like a complete response. Breaking the
		// connection is what the client would have seen without a proxy in the way.
		panic(http.ErrAbortHandler)
	}
}

// clientHungUp reports that the copy stopped because the client went away rather than because
// anything went wrong. A browser that has seen enough of an image, a player that closes a stream and
// a cancelled fetch all end this way, and calling that an error puts a red row next to a response
// the app got exactly as much of as it wanted.
func clientHungUp(ctx context.Context, err error) bool {
	return ctx.Err() != nil && errors.Is(err, context.Canceled)
}

// flushWriter pushes each chunk to the client as it arrives so streamed responses stay live.
type flushWriter struct {
	w  io.Writer
	rc *http.ResponseController
}

func (fw *flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if err == nil {
		_ = fw.rc.Flush()
	}
	return n, err
}

// capture keeps the first max bytes of what passes through it and counts the rest.
type capture struct {
	max       int
	buf       []byte
	n         int64
	truncated bool
}

func (c *capture) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	room := c.max - len(c.buf)
	switch {
	case room >= len(p):
		c.buf = append(c.buf, p...)
	case room > 0:
		c.buf = append(c.buf, p[:room]...)
		c.truncated = true
	default:
		c.truncated = true
	}
	return len(p), nil
}

// websocket hands the upgrade to the upstream and then copies bytes both ways; only the handshake is recorded.
func (s *Server) websocket(w http.ResponseWriter, r *http.Request, scheme string) {
	ctx := r.Context()
	host := r.Host
	f := s.newFlow(ctx, KindWebSocket, r.Method, scheme, host, r.URL.RequestURI())
	f.update(func(f *Flow) { f.ReqHeader = r.Header.Clone() })
	fail := func(status int, err error) {
		s.finish(ctx, f, func(f *Flow) { f.Error = err.Error() })
		http.Error(w, "sims proxy: "+err.Error(), status)
	}
	upstream, err := s.dial(ctx, scheme, host)
	if err != nil {
		fail(http.StatusBadGateway, err)
		return
	}
	defer upstream.Close()
	out := r.Clone(ctx)
	out.RequestURI = ""
	out.URL.Scheme, out.URL.Host, out.Host = scheme, host, host
	// the upgrade keeps Connection and Upgrade, but not what the client sent to the proxy itself
	stripProxyOnly(out.Header)
	if err := out.Write(upstream); err != nil {
		fail(http.StatusBadGateway, err)
		return
	}
	br := bufio.NewReader(upstream)
	resp, err := http.ReadResponse(br, out)
	if err != nil {
		fail(http.StatusBadGateway, err)
		return
	}
	client, cbuf, err := http.NewResponseController(w).Hijack()
	if err != nil {
		fail(http.StatusInternalServerError, err)
		return
	}
	defer client.Close()
	if !s.track(client) {
		return
	}
	defer s.untrack(client)
	if err := resp.Write(cbuf); err == nil {
		err = cbuf.Flush()
	}
	s.finish(ctx, f, func(f *Flow) {
		f.Status, f.StatusText = resp.StatusCode, http.StatusText(resp.StatusCode)
		f.RespHeader = resp.Header.Clone()
	})
	if resp.StatusCode != http.StatusSwitchingProtocols {
		return
	}
	pipe(client, cbuf.Reader, upstream, br)
}

func (s *Server) dial(ctx context.Context, scheme, host string) (net.Conn, error) {
	// an IPv6 literal is full of colons, so appending ":443" to a bare one produces an address that
	// no longer parses and a TLS handshake with an empty server name
	name, port, err := net.SplitHostPort(host)
	if err != nil {
		name = host
		port = "80"
		if scheme == "https" {
			port = "443"
		}
		host = net.JoinHostPort(name, port)
	}
	d := &net.Dialer{Timeout: 30 * time.Second}
	if scheme == "https" {
		return (&tls.Dialer{NetDialer: d, Config: &tls.Config{ServerName: name}}).DialContext(ctx, "tcp", host)
	}
	return d.DialContext(ctx, "tcp", host)
}

// pipe copies until either side closes; buffered bytes already read from each side go first.
func pipe(a net.Conn, ar io.Reader, b net.Conn, br io.Reader) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(a, br); done <- struct{}{} }()
	go func() { _, _ = io.Copy(b, ar); done <- struct{}{} }()
	<-done
	_ = a.Close()
	_ = b.Close()
	<-done
}

// handleConnect answers the CONNECT and then looks at the first byte: a TLS ClientHello is terminated
// here with a leaf for the requested host; anything else is tunnelled untouched.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request, ignore bool) {
	// The CONNECT's own context dies when this handler returns, which is long before the tunnel does.
	// Requests inside the tunnel outlive it, so they get one tied to the server rather than to the
	// CONNECT; keeping the connInfo means they are still attributed to the same client.
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()
	target := r.Host
	if _, _, err := net.SplitHostPort(target); err != nil {
		target += ":443"
	}
	client, buf, err := http.NewResponseController(w).Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer client.Close()
	if !s.track(client) {
		return // the capture is stopping; do not start relaying anything new
	}
	defer s.untrack(client)
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if ignore {
		// not our traffic: relay the bytes and record nothing
		s.passThrough(ctx, client, buf.Reader, target)
		return
	}
	_ = client.SetReadDeadline(time.Now().Add(peekTimeout))
	first, err := buf.Reader.Peek(1)
	_ = client.SetReadDeadline(time.Time{})
	if err != nil || first[0] != 0x16 {
		s.tunnel(ctx, client, buf.Reader, target, "the client did not start TLS")
		return
	}
	host, _, _ := net.SplitHostPort(target)
	conn := &bufferedConn{Conn: client, r: buf.Reader}
	// TLS 1.0 clients still exist on older devices and inside old SDKs; refusing them here would show
	// up as a failed request rather than as traffic the proxy chose not to read.
	tlsConn := tls.Server(conn, &tls.Config{
		MinVersion: tls.VersionTLS10,
		NextProtos: []string{"http/1.1"},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := hello.ServerName
			if name == "" {
				name = host
			}
			return s.CA.Leaf(name)
		},
	})
	_ = tlsConn.SetDeadline(time.Now().Add(handshakeTimeout))
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		f := s.newFlow(ctx, KindTunnel, http.MethodConnect, "https", target, "")
		s.finish(ctx, f, func(f *Flow) {
			f.Error = "TLS handshake failed: " + err.Error() + " (the device does not trust the sims CA, or the app pins its certificate)"
		})
		return
	}
	_ = tlsConn.SetDeadline(time.Time{})

	inner := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host == "" {
				r.Host = host
			}
			s.forward(w, r, "https")
		}),
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	_ = inner.Serve(newOneConnListener(tlsConn))
}

// isUpgrade reports whether the request asks to leave HTTP behind.
func isUpgrade(r *http.Request) bool {
	return r.Header.Get("Upgrade") != ""
}

// relayUpgrade hands an ignored upgrade to the origin byte for byte. Nothing is decoded and nothing
// is recorded: for traffic sims was not asked to watch, the proxy has to be invisible.
func (s *Server) relayUpgrade(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	scheme := r.URL.Scheme
	if scheme == "" {
		scheme = "http"
	}
	upstream, err := s.dial(r.Context(), scheme, host)
	if err != nil {
		http.Error(w, "sims proxy: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer upstream.Close()

	out := r.Clone(r.Context())
	out.RequestURI = ""
	out.URL.Scheme, out.URL.Host, out.Host = scheme, host, host
	stripProxyOnly(out.Header)
	if err := out.Write(upstream); err != nil {
		http.Error(w, "sims proxy: "+err.Error(), http.StatusBadGateway)
		return
	}
	client, buf, err := http.NewResponseController(w).Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer client.Close()
	if !s.track(client) {
		return
	}
	defer s.untrack(client)
	pipe(client, buf.Reader, upstream, upstream)
}

// relay forwards a plain request for traffic sims was not asked to watch, recording nothing.
func (s *Server) relay(w http.ResponseWriter, r *http.Request) {
	out := r.Clone(r.Context())
	out.RequestURI = ""
	if out.URL.Host == "" {
		out.URL.Host = r.Host
	}
	stripHopByHop(out.Header)
	resp, err := s.Transport.RoundTrip(out)
	if err != nil {
		http.Error(w, "sims proxy: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	stripHopByHop(resp.Header)
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	rc := http.NewResponseController(w)
	_ = rc.Flush()
	_, _ = io.Copy(&flushWriter{w: w, rc: rc}, resp.Body)
}

// passThrough relays a CONNECT for traffic sims was not asked to watch. Nothing is decrypted and
// nothing is recorded, so an app on this machine behaves exactly as it would with no proxy at all.
func (s *Server) passThrough(ctx context.Context, client net.Conn, clientBuf io.Reader, target string) {
	upstream, err := (&net.Dialer{Timeout: 30 * time.Second}).DialContext(ctx, "tcp", target)
	if err != nil {
		return
	}
	defer upstream.Close()
	pipe(client, clientBuf, upstream, upstream)
}

// tunnel copies a CONNECT's bytes through untouched and records it as one flow.
func (s *Server) tunnel(ctx context.Context, client net.Conn, clientBuf io.Reader, target, why string) {
	f := s.newFlow(ctx, KindTunnel, http.MethodConnect, "tcp", target, "")
	upstream, err := (&net.Dialer{Timeout: 30 * time.Second}).DialContext(ctx, "tcp", target)
	if err != nil {
		s.finish(ctx, f, func(f *Flow) { f.Error = err.Error() })
		return
	}
	defer upstream.Close()
	up := &countingConn{Conn: upstream}
	pipe(client, clientBuf, up, up)
	s.finish(ctx, f, func(f *Flow) {
		f.Status, f.StatusText = http.StatusOK, "tunnelled: "+why
		f.ReqSize, f.RespSize = up.written, up.read
	})
}

type countingConn struct {
	net.Conn
	read, written int64
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.read += int64(n)
	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.written += int64(n)
	return n, err
}

// bufferedConn puts the peeked bytes back in front of the connection for the TLS server.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// oneConnListener hands out a single connection and then blocks until it closes, so the http.Server
// serving it lives exactly as long as the connection does.
type oneConnListener struct {
	conn   net.Conn
	once   sync.Once
	closed chan struct{}
}

func newOneConnListener(c net.Conn) *oneConnListener {
	l := &oneConnListener{closed: make(chan struct{})}
	l.conn = &notifyingConn{Conn: c, onClose: func() { l.once.Do(func() { close(l.closed) }) }}
	return l
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	if l.conn != nil {
		c := l.conn
		l.conn = nil
		return c, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}

func (l *oneConnListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *oneConnListener) Addr() net.Addr { return dummyAddr("tunnel") }

type notifyingConn struct {
	net.Conn
	onClose func()
}

func (c *notifyingConn) Close() error {
	err := c.Conn.Close()
	c.onClose()
	return err
}

type dummyAddr string

func (a dummyAddr) Network() string { return string(a) }
func (a dummyAddr) String() string  { return string(a) }

// String describes the server for a status line.
func (s *Server) String() string {
	if s.ln == nil {
		return "sims proxy (not listening)"
	}
	return fmt.Sprintf("sims proxy on %s", s.ln.Addr())
}

// RecordForTest puts a finished flow in the store as if it had gone through the proxy. It exists so
// tests of the layers above can fill a store without standing up real traffic.
func (s *Server) RecordForTest(f Flow) {
	s.init()
	l := &liveFlow{f: f}
	s.Store.add(l)
	s.Store.touch(l)
}

// DialForTest reaches an upstream the way the proxy does.
func (s *Server) DialForTest(ctx context.Context, scheme, host string) (net.Conn, error) {
	return s.dial(ctx, scheme, host)
}
