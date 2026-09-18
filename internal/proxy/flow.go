package proxy

import (
	"net/http"
	"slices"
	"sync"
	"time"
)

// Origin says which side of the proxy a flow came from when the proxy can tell: the device it was
// started for, or something else on this machine that shares the system proxy.
type Origin string

const (
	OriginUnknown Origin = ""
	OriginDevice  Origin = "device"
	OriginHost    Origin = "host"
)

type Kind string

const (
	KindHTTP      Kind = "http"
	KindWebSocket Kind = "websocket"
	// KindTunnel is a CONNECT whose bytes were passed through untouched: the client did not speak TLS, or it did and the handshake failed.
	KindTunnel Kind = "tunnel"
)

// Flow is one exchange through the proxy, as a plain value: the live one is held by a liveFlow and
// copied out with Snapshot, so a reader never shares state with the goroutine still writing it.
type Flow struct {
	ID       int64         `json:"id"`
	Kind     Kind          `json:"kind"`
	Start    time.Time     `json:"start"`
	Duration time.Duration `json:"duration"` // zero until Done
	Done     bool          `json:"done"`

	Client  string `json:"client"`            // remote address of the connection
	Process string `json:"process,omitempty"` // owner of that connection on this machine, when known
	Origin  Origin `json:"origin,omitempty"`

	Method string `json:"method"`
	URL    string `json:"url"` // scheme://host/path?query
	Host   string `json:"host"`
	Path   string `json:"path"`

	Status     int         `json:"status,omitempty"`
	StatusText string      `json:"statusText,omitempty"`
	ReqHeader  http.Header `json:"requestHeaders,omitempty"`
	RespHeader http.Header `json:"responseHeaders,omitempty"`
	ReqBody    []byte      `json:"-"`
	RespBody   []byte      `json:"-"`
	// sizes count what actually went through; the bodies above stop at the capture limit
	ReqSize       int64 `json:"requestSize"`
	RespSize      int64 `json:"responseSize"`
	ReqTruncated  bool  `json:"requestTruncated,omitempty"`
	RespTruncated bool  `json:"responseTruncated,omitempty"`

	Error string `json:"error,omitempty"`
}

// liveFlow guards one Flow while the connection that owns it is still running.
type liveFlow struct {
	mu sync.RWMutex
	f  Flow
}

func (l *liveFlow) Snapshot() Flow {
	l.mu.RLock()
	defer l.mu.RUnlock()
	c := l.f
	c.ReqHeader = l.f.ReqHeader.Clone()
	c.RespHeader = l.f.RespHeader.Clone()
	return c
}

func (l *liveFlow) update(fn func(*Flow)) {
	l.mu.Lock()
	fn(&l.f)
	l.mu.Unlock()
}

func (l *liveFlow) done() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.f.Done
}

func (l *liveFlow) id() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.f.ID
}

// Store keeps the most recent flows and tells listeners when anything changed.
type Store struct {
	mu      sync.Mutex
	flows   []*liveFlow
	max     int
	seq     int64
	changed chan struct{}
	onDone  []func(Flow)
}

const DefaultMaxFlows = 2000

func NewStore(maxFlows int) *Store {
	if maxFlows <= 0 {
		maxFlows = DefaultMaxFlows
	}
	return &Store{max: maxFlows, changed: make(chan struct{})}
}

func (s *Store) add(l *liveFlow) {
	s.mu.Lock()
	s.seq++
	id := s.seq
	s.flows = append(s.flows, l)
	if len(s.flows) > s.max {
		s.flows = s.flows[len(s.flows)-s.max:]
	}
	s.bump()
	s.mu.Unlock()
	l.update(func(f *Flow) { f.ID = id })
}

// touch is called by the proxy after every visible change to a flow; the store only relays it.
func (s *Store) touch(l *liveFlow) {
	s.mu.Lock()
	s.bump()
	handlers := slices.Clone(s.onDone)
	s.mu.Unlock()
	if len(handlers) == 0 || !l.done() {
		return
	}
	snap := l.Snapshot()
	for _, h := range handlers {
		h(snap)
	}
}

func (s *Store) bump() {
	close(s.changed)
	s.changed = make(chan struct{})
}

// Changed returns a channel closed on the next change; take a fresh one after each wake-up.
func (s *Store) Changed() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changed
}

// OnDone runs fn with a snapshot of every flow as it completes. Register before traffic starts.
func (s *Store) OnDone(fn func(Flow)) {
	s.mu.Lock()
	s.onDone = append(s.onDone, fn)
	s.mu.Unlock()
}

// Flows returns snapshots of every flow kept, oldest first.
func (s *Store) Flows() []Flow {
	s.mu.Lock()
	live := slices.Clone(s.flows)
	s.mu.Unlock()
	out := make([]Flow, len(live))
	for i, l := range live {
		out[i] = l.Snapshot()
	}
	return out
}

// Get returns one flow by id.
func (s *Store) Get(id int64) (Flow, bool) {
	s.mu.Lock()
	live := slices.Clone(s.flows)
	s.mu.Unlock()
	for _, l := range live {
		if l.id() == id {
			return l.Snapshot(), true
		}
	}
	return Flow{}, false
}

func (s *Store) Clear() {
	s.mu.Lock()
	s.flows = nil
	s.bump()
	s.mu.Unlock()
}

func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.flows)
}
