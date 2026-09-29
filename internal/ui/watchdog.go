package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

const (
	watchEvery    = 2 * time.Second
	watchPatience = 10 * time.Second
	stopGrace     = 3 * time.Second
)

type hangDumps struct {
	mu    sync.Mutex
	dir   string
	paths []string
}

func newHangDumps() *hangDumps {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return &hangDumps{dir: filepath.Join(dir, "sims")}
}

func (h *hangDumps) write() (string, error) {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, len(buf)*2)
	}
	if err := os.MkdirAll(h.dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(h.dir, "hang-"+time.Now().Format("20060102-150405")+".txt")
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		return "", err
	}
	h.mu.Lock()
	h.paths = append(h.paths, path)
	h.mu.Unlock()
	return path, nil
}

func (h *hangDumps) written() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.paths...)
}

// The stacks go to a file because a UI that answers nothing cannot show them.
// An unanswered ping is waited for rather than abandoned: its goroutine stays parked until the queue drains, so a stall does not leak one per tick.
func (a *App) watchUI(ctx context.Context, every, patience time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		answered := make(chan struct{})
		go func() {
			a.tv.QueueUpdate(func() {})
			close(answered)
		}()
		select {
		case <-answered:
			continue
		case <-time.After(patience):
		}
		a.hangs.write()
		select {
		case <-answered:
		case <-ctx.Done():
			return
		}
	}
}

// A Stop the UI goroutine cannot act on would leave the process hanging with the terminal still in raw mode, so ctrl+c leaves without it.
// The terminal is still raw here, hence the \r before each newline.
func (a *App) leaveStuck() {
	if len(a.hangs.written()) == 0 {
		a.hangs.write()
	}
	fmt.Fprintln(os.Stderr, "\r\nsims: the screen stopped responding; every goroutine's stack is in:")
	for _, p := range a.hangs.written() {
		fmt.Fprintf(os.Stderr, "  %s\r\n", p)
	}
	os.Exit(2)
}
