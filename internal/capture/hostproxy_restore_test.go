package capture

import (
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
	if err := h.set(t.Context(), "127.0.0.1", 65123); err != nil {
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
