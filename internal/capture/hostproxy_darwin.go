package capture

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// hostProxy drives this Mac's own proxy settings, which is what a simulator follows. It remembers
// what it found so the machine can be put back exactly as it was.
type hostProxy struct {
	service string
	before  []netsetupState
}

type netsetupState struct {
	kind    string // "webproxy" or "securewebproxy"
	enabled bool
	server  string
	port    int
}

// activeService is the network service carrying the default route: the one a simulator's traffic
// leaves by, and so the only one that needs changing.
func activeService(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "route", "-n", "get", "default").Output()
	if err != nil {
		return "", fmt.Errorf("no default route: %w", err)
	}
	iface := ""
	for _, line := range strings.Split(string(out), "\n") {
		if name, ok := strings.CutPrefix(strings.TrimSpace(line), "interface:"); ok {
			iface = strings.TrimSpace(name)
		}
	}
	if iface == "" {
		return "", fmt.Errorf("no interface on the default route")
	}
	order, err := exec.CommandContext(ctx, "networksetup", "-listnetworkserviceorder").Output()
	if err != nil {
		return "", err
	}
	// each service is a pair of lines: "(n) Name" then "(Hardware Port: ..., Device: enX)"
	name := ""
	for _, line := range strings.Split(string(order), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "(") && strings.Contains(line, ")") && !strings.Contains(line, "Hardware Port") {
			if i := strings.Index(line, ")"); i >= 0 {
				name = strings.TrimSpace(line[i+1:])
			}
			continue
		}
		if strings.Contains(line, "Device: "+iface+")") && name != "" {
			return name, nil
		}
	}
	return "", fmt.Errorf("no network service uses %s", iface)
}

func readHostProxy(ctx context.Context, service, kind string) (netsetupState, error) {
	out, err := exec.CommandContext(ctx, "networksetup", "-get"+kind, service).Output()
	if err != nil {
		return netsetupState{}, err
	}
	st := netsetupState{kind: kind}
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "Enabled":
			st.enabled = strings.EqualFold(v, "yes")
		case "Server":
			st.server = v
		case "Port":
			st.port, _ = strconv.Atoi(v)
		}
	}
	return st, nil
}

// set points the machine's web and secure web proxies at addr, remembering what was there.
func (h *hostProxy) set(ctx context.Context, host string, port int) error {
	service, err := activeService(ctx)
	if err != nil {
		return err
	}
	h.service = service
	for _, kind := range []string{"webproxy", "securewebproxy"} {
		before, err := readHostProxy(ctx, service, kind)
		if err != nil {
			return err
		}
		h.before = append(h.before, before)
		if err := exec.CommandContext(ctx, "networksetup", "-set"+kind, service, host, strconv.Itoa(port)).Run(); err != nil {
			return fmt.Errorf("could not point %s at the proxy: %w", service, err)
		}
	}
	return nil
}

// restore puts the machine back. It runs on a background context so a cancelled capture still
// undoes itself: a Mac left pointing at a proxy that has stopped cannot reach the network.
//
// A proxy that was off keeps its stored address in the settings pane, and a browser that reads the
// settings while a capture is running can hold on to that address afterwards. So restoring an
// already-off proxy clears the address as well as the state, leaving nothing to be picked up.
func (h *hostProxy) restore() error {
	if h.service == "" {
		return nil
	}
	ctx := context.Background()
	var firstErr error
	fail := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, st := range h.before {
		if st.enabled && st.server != "" {
			fail(exec.CommandContext(ctx, "networksetup", "-set"+st.kind, h.service, st.server, strconv.Itoa(st.port)).Run())
			continue
		}
		// -set<kind> writes the address and turns it on, so the state goes off afterwards
		fail(exec.CommandContext(ctx, "networksetup", "-set"+st.kind, h.service, "", "0").Run())
		fail(exec.CommandContext(ctx, "networksetup", "-set"+st.kind+"state", h.service, "off").Run())
	}
	h.service, h.before = "", nil
	return firstErr
}

func hostProxySupported() bool { return true }
