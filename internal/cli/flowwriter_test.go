package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/siner308/sims/internal/proxy"
)

// Flows complete on one goroutine per proxied connection. An unsynchronised writer loses whole
// records, and a script reading --json as one record per line gets a short set with exit code 0.
func TestFlowWriterKeepsEveryRecord(t *testing.T) {
	var buf bytes.Buffer
	w := &flowWriter{out: &buf, json: true}

	const n = 200
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			w.write(proxy.Flow{ID: int64(i), Method: "GET", URL: "https://example.com/", Status: 200, Done: true})
		})
	}
	wg.Wait()

	lines := 0
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var f proxy.Flow
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			t.Fatalf("line %d is not a record: %v", lines+1, err)
		}
		lines++
	}
	if lines != n {
		t.Errorf("wrote %d records for %d flows", lines, n)
	}
}

// The human form must not interleave either.
func TestFlowWriterLinesStayWhole(t *testing.T) {
	var buf bytes.Buffer
	w := &flowWriter{out: &buf}

	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			w.write(proxy.Flow{Method: "GET", URL: "https://example.com/some/path", Status: 200, Done: true})
		})
	}
	wg.Wait()

	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if !strings.Contains(line, "https://example.com/some/path") {
			t.Fatalf("a line was torn: %q", line)
		}
	}
}
