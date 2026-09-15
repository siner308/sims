package android

import "testing"

func TestApplyConfig(t *testing.T) {
	in := "avd.ini.encoding=UTF-8\nhw.keyboard = no\nhw.lcd.density = 420\n"
	out, changed := applyConfig(in, map[string]string{"hw.keyboard": "yes"})
	if !changed {
		t.Fatal("expected a change")
	}
	want := "avd.ini.encoding=UTF-8\nhw.keyboard = yes\nhw.lcd.density = 420\n"
	if out != want {
		t.Errorf("got %q, want %q", out, want)
	}

	out2, changed := applyConfig(out, map[string]string{"hw.keyboard": "yes"})
	if changed || out2 != out {
		t.Error("second apply should be a no-op")
	}

	out3, changed := applyConfig("hw.lcd.density = 420\n", map[string]string{"hw.keyboard": "yes"})
	if !changed || out3 != "hw.lcd.density = 420\nhw.keyboard = yes\n" {
		t.Errorf("missing key should be appended, got %q", out3)
	}
}

func TestAVDDefaults_PlayStoreOnlyForPlayImages(t *testing.T) {
	play := avdDefaults("tag.id = google_apis_playstore\nhw.keyboard = no\n")
	if play["PlayStore.enabled"] != "yes" || play["hw.keyboard"] != "yes" || play["hw.gpu.enabled"] != "yes" || play["hw.camera.front"] != "emulated" {
		t.Errorf("play defaults = %v", play)
	}
	ps16k := avdDefaults("tag.id = page_size_16kb\ntag.ids = page_size_16kb,google_apis_playstore\n")
	if ps16k["PlayStore.enabled"] != "yes" {
		t.Errorf("16k Play image should enable Play Store: %v", ps16k)
	}
	plain := avdDefaults("tag.id = google_apis\n")
	if _, has := plain["PlayStore.enabled"]; has {
		t.Errorf("PlayStore.enabled must not be forced on a non-Play image: %v", plain)
	}
}
