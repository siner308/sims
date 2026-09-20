package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
)

// A client that stops reading is not a failure: it got what it asked for. Recording it as one put a
// red row next to a response the app had finished with.
func TestAClientHangingUpIsNotAnError(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	if !clientHungUp(cancelled, context.Canceled) {
		t.Error("a cancelled request with a cancelled copy was not recognised as the client leaving")
	}
	// a real failure still counts as one, even on a request that was cancelled afterwards
	if clientHungUp(cancelled, io.ErrUnexpectedEOF) {
		t.Error("an upstream failure was written off as the client leaving")
	}
	// and a cancellation with the request still live is the upstream, not the client
	if clientHungUp(context.Background(), context.Canceled) {
		t.Error("a cancel with the request still live was blamed on the client")
	}
	// the test is errors.Is, so a wrapped cancellation counts and a lookalike message does not
	if !clientHungUp(cancelled, fmt.Errorf("write tcp: %w", context.Canceled)) {
		t.Error("a wrapped cancellation was not recognised")
	}
	if clientHungUp(cancelled, errors.New("context canceled")) {
		t.Error("an unrelated error was matched on its message alone")
	}
}
