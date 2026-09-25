package android

import (
	"strings"
	"testing"
)

func TestAvdmanagerCmd_PointsToolsdirIntoSDK(t *testing.T) {
	if isWindows {
		t.Skip("quoting differs on windows")
	}
	t.Setenv("AVDMANAGER_OPTS", "-Xmx1g")
	cmd := sdk{root: "/sdk dir's"}.avdmanagerCmd(t.Context(), "list", "device")
	var opts string
	for _, kv := range cmd.Env {
		if v, ok := strings.CutPrefix(kv, "AVDMANAGER_OPTS="); ok {
			opts = v
		}
	}
	want := `'-Dcom.android.sdkmanager.toolsdir=/sdk dir'\''s/cmdline-tools/latest' -Xmx1g`
	if opts != want {
		t.Errorf("AVDMANAGER_OPTS = %q, want %q", opts, want)
	}
}
