package capture

import (
	"testing"
	"time"

	"github.com/siner308/sims/internal/device"
)

func TestPhoneRecordRoundTripsAndMatchesOnlyTheSameTarget(t *testing.T) {
	dir := t.TempDir()
	d := device.Device{ID: "phone-1", Name: "phone", Platform: device.PlatformIOS, Kind: device.KindPhysical}
	if _, ok := Phone(dir, d.ID); ok {
		t.Fatal("a phone nothing was sent to has a record")
	}
	rec := PhoneRecord{Device: d, Host: "10.0.0.5", Port: PhonePort, SSID: "Home", CertFingerprint: "abc", InstalledAt: time.Now()}
	if err := writePhone(dir, rec); err != nil {
		t.Fatal(err)
	}
	got, ok := Phone(dir, d.ID)
	if !ok || got.Port != PhonePort || got.SSID != "Home" {
		t.Fatalf("record read back as %+v, %v", got, ok)
	}
	if !got.Matches("10.0.0.5", PhonePort, "Home", "abc") {
		t.Error("the record does not match the target it was written for")
	}
	for name, ok := range map[string]bool{
		"host":  got.Matches("10.0.0.6", PhonePort, "Home", "abc"),
		"port":  got.Matches("10.0.0.5", PhonePort+1, "Home", "abc"),
		"ssid":  got.Matches("10.0.0.5", PhonePort, "Office", "abc"),
		"cert":  got.Matches("10.0.0.5", PhonePort, "Home", "def"),
		"empty": PhoneRecord{}.Matches("10.0.0.5", PhonePort, "Home", "abc"),
	} {
		if ok {
			t.Errorf("a record with a different %s still matched; the phone would be left pointing at the wrong place", name)
		}
	}
	if all := PhoneRecords(dir); len(all) != 1 || all[0].Device.ID != d.ID {
		t.Errorf("PhoneRecords = %+v", all)
	}
	if err := ForgetPhone(dir, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := Phone(dir, d.ID); ok {
		t.Error("the record survived ForgetPhone")
	}
	if err := ForgetPhone(dir, d.ID); err != nil {
		t.Errorf("forgetting twice is an error: %v", err)
	}
}
