package proxy

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LookupProcess finds the process on this machine that owns the client end of a TCP connection.
// lsof is slow, so callers run it off the request path and accept that a short-lived connection may
// be gone before it answers.
func LookupProcess(ctx context.Context, clientAddr string) (Process, error) {
	_, port, err := net.SplitHostPort(clientAddr)
	if err != nil {
		return Process{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP:"+port, "-sTCP:ESTABLISHED", "-Fpcn").Output()
	if err != nil {
		return Process{}, err
	}
	// Both ends of a loopback connection show up here, and one of them is this process holding the
	// accepted socket. The client is the record whose LOCAL address is the one the server sees as
	// remote; the accepted side has it on the right of the arrow instead.
	var cur Process
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			cur = Process{}
			cur.PID, _ = strconv.Atoi(line[1:])
		case 'c':
			cur.Name = line[1:]
		case 'n':
			local, _, ok := strings.Cut(line[1:], "->")
			if !ok || local != clientAddr || cur.PID == 0 {
				continue
			}
			if path := executablePath(ctx, cur.PID); path != "" {
				cur.Path, cur.Name = path, filepath.Base(path)
			}
			return cur, nil
		}
	}
	return Process{}, errors.New("no process owns " + clientAddr)
}

func executablePath(ctx context.Context, pid int) string {
	out, err := exec.CommandContext(ctx, "ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
