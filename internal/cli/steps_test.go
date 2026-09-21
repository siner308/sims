package cli

import (
	"bytes"
	"testing"

	"github.com/siner308/sims/internal/device"
)

func TestManualStepsPrintTheirTapsUnderTheRow(t *testing.T) {
	var out bytes.Buffer
	printSteps(&out, []device.ProxyStep{
		{Title: "profile", Detail: "sent to the phone"},
		{Title: "approve", Detail: "install the profile", Manual: true, Todo: []string{"open Settings", "tap Install"}},
	})
	want := "   profile      sent to the phone\n" +
		" ! approve      install the profile\n" +
		"                1. open Settings\n" +
		"                2. tap Install\n"
	if out.String() != want {
		t.Errorf("steps printed as:\n%s\nwant:\n%s", out.String(), want)
	}
}
