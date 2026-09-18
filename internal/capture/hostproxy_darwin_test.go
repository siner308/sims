package capture

import (
	"os/exec"
	"strings"
	"testing"
)

// The service carrying the default route is the one a simulator's traffic leaves by; naming the
// wrong one would change a setting that does nothing and leave the real one untouched.
func TestActiveServiceIsAKnownService(t *testing.T) {
	svc, err := activeService(t.Context())
	if err != nil {
		t.Skip(err)
	}
	out, err := exec.Command("networksetup", "-listallnetworkservices").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(strings.TrimPrefix(line, "*")) == svc {
			t.Logf("active service: %q", svc)
			return
		}
	}
	t.Errorf("activeService returned %q, which networksetup does not list", svc)
}

// Reading a proxy setting must round-trip, or restore would put back something else.
func TestReadHostProxyParses(t *testing.T) {
	svc, err := activeService(t.Context())
	if err != nil {
		t.Skip(err)
	}
	for _, kind := range []string{"webproxy", "securewebproxy"} {
		st, err := readHostProxy(t.Context(), svc, kind)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: enabled=%v server=%q port=%d", kind, st.enabled, st.server, st.port)
		if st.enabled && st.server == "" {
			t.Errorf("%s reads as enabled with no server", kind)
		}
	}
}
