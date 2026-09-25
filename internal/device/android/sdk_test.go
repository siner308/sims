package android

import (
	"strings"
	"testing"
)

func TestTail(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"[====    ] 25% Loading local repository...       \rError: Package path is not valid. Valid system image paths are:\nnull\n",
			"Error: Package path is not valid. Valid system image paths are: null"},
		{"first\nsecond\n", "second"},
		{"50%\r100%\r", "100%"},
		{"  \n", ""},
	} {
		if got := tail([]byte(tc.in)); got != tc.want {
			t.Errorf("tail(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

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
