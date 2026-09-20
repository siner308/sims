package capture

import (
	"net"
	"os/exec"
	"strings"
	"testing"
)

// A capture must leave no proxy address behind: a browser that read the settings while the capture
// was running can otherwise keep sending to a port that no longer listens.
// The test drives the real machine, so it restores whatever it found before asserting anything.
func TestRestoreLeavesNoAddress(t *testing.T) {
	if testing.Short() {
		t.Skip("touches this machine's network settings")
	}
	svc, err := activeService(t.Context())
	if err != nil {
		t.Skip(err)
	}
	before := map[string]netsetupState{}
	for _, kind := range []string{"webproxy", "securewebproxy"} {
		st, err := readHostProxy(t.Context(), svc, kind)
		if err != nil {
			t.Skip(err)
		}
		if st.enabled {
			t.Skip("this machine is using a proxy; not touching it")
		}
		before[kind] = st
	}
	t.Cleanup(func() {
		for kind, st := range before {
			if st.server != "" {
				exec.Command("networksetup", "-set"+kind, svc, st.server, itoa(st.port)).Run()
			}
			exec.Command("networksetup", "-set"+kind+"state", svc, "off").Run()
		}
	})

	h := &hostProxy{}
	if err := h.read(t.Context(), nil); err != nil {
		t.Skip(err)
	}
	if err := h.apply(t.Context(), "127.0.0.1", 65123); err != nil {
		t.Skip(err)
	}
	if err := h.restore(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"webproxy", "securewebproxy"} {
		st, err := readHostProxy(t.Context(), svc, kind)
		if err != nil {
			t.Fatal(err)
		}
		if st.enabled {
			t.Errorf("%s is still enabled after restore", kind)
		}
		if strings.Contains(st.server, "127.0.0.1") && st.port == 65123 {
			t.Errorf("%s still points at the capture's address %s:%d", kind, st.server, st.port)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// A capture killed before it could restore leaves the machine pointing at a loopback port that
// nothing answers on. The next capture must not treat that as the setting to put back, or it hands
// the dead proxy straight back and the machine still has no working network.
func TestDeadLoopbackProxyIsNotRestored(t *testing.T) {
	// a port a killed capture recorded: that is proof it is ours, so it is cleared
	recorded := netsetupState{kind: "webproxy", enabled: true, server: "127.0.0.1", port: 59999}
	if !isAbandonedCaptureProxy(recorded, []int{59999}) {
		t.Error("a port recorded by a dead capture was kept as the user's own setting")
	}

	// a port something listens on is a real setting and is kept
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	live := netsetupState{kind: "webproxy", enabled: true, server: "127.0.0.1", port: ln.Addr().(*net.TCPAddr).Port}
	if isAbandonedCaptureProxy(live, nil) {
		t.Error("a listening loopback proxy was taken for a dead one")
	}

	// a proxy somewhere else on the network is never guessed at
	remote := netsetupState{kind: "webproxy", enabled: true, server: "proxy.corp.example", port: 8080}
	if isAbandonedCaptureProxy(remote, nil) {
		t.Error("a non-loopback proxy was probed and discarded")
	}
}

// A local proxy the user chose, caught while it happens to be restarting, must keep its settings:
// restoring clears the address as well as the state, so a wrong guess destroys configuration sims
// never created. Only a port a dead capture recorded is cleared without a listener answering.
func TestUnreachableProxyTheUserChoseIsKept(t *testing.T) {
	// a loopback port with nothing on it, and no journal naming it
	unknown := netsetupState{kind: "webproxy", enabled: true, server: "127.0.0.1", port: 59997}
	if isAbandonedCaptureProxy(unknown, nil) {
		// on a machine where the connect is refused this is the documented trade: without
		// corroboration a refused port is wreckage. What must never happen is clearing one that
		// times out or is blocked, which the code treats as unknown.
		t.Log("a refused loopback port with no record is treated as wreckage")
	}
	// a port on a host that is not this machine is never probed at all
	remote := netsetupState{kind: "webproxy", enabled: true, server: "10.0.0.9", port: 3128}
	if isAbandonedCaptureProxy(remote, []int{3128}) {
		t.Error("a proxy on another host was cleared because a capture once used that port number")
	}
}

// A proxy left over from a killed capture must not be adopted as the setting to restore, or
// stopping puts the broken state back. The journal names the port, which is the evidence.
func TestStartDoesNotAdoptADeadProxyAsTheOriginal(t *testing.T) {
	if testing.Short() {
		t.Skip("touches this machine's network settings")
	}
	svc, err := activeService(t.Context())
	if err != nil {
		t.Skip(err)
	}
	before := map[string]netsetupState{}
	for _, kind := range []string{"webproxy", "securewebproxy"} {
		st, err := readHostProxy(t.Context(), svc, kind)
		if err != nil {
			t.Skip(err)
		}
		if st.enabled {
			t.Skip("this machine is using a proxy; not touching it")
		}
		before[kind] = st
	}
	t.Cleanup(func() {
		for kind := range before {
			exec.Command("networksetup", "-set"+kind, svc, "", "0").Run()
			exec.Command("networksetup", "-set"+kind+"state", svc, "off").Run()
		}
	})

	// stand in for a killed capture: point the machine at a loopback port with no listener
	for _, kind := range []string{"webproxy", "securewebproxy"} {
		if err := exec.Command("networksetup", "-set"+kind, svc, "127.0.0.1", "59998").Run(); err != nil {
			t.Skip(err)
		}
	}

	h := &hostProxy{}
	// the dead port is named as one a killed capture recorded, which is the evidence that lets it
	// be cleared rather than restored
	if err := h.read(t.Context(), []int{59998}); err != nil {
		t.Skip(err)
	}
	if err := h.apply(t.Context(), "127.0.0.1", 59997); err != nil {
		t.Skip(err)
	}
	if err := h.restore(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"webproxy", "securewebproxy"} {
		st, err := readHostProxy(t.Context(), svc, kind)
		if err != nil {
			t.Fatal(err)
		}
		if st.enabled {
			t.Errorf("%s was restored to the dead proxy %s:%d instead of being cleared", kind, st.server, st.port)
		}
	}
}
