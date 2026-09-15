package android

import "testing"

func TestParseBadging(t *testing.T) {
	out := "package: name='com.devsisters.test.whatever' versionCode='23642' versionName='10.1.02' platformBuildVersionName='16'\n" +
		"application-label:'Nightmare'\n" +
		"application-label-af:'Nightmare'\n" +
		"application-label-ko:'악몽'\n"
	info := parseBadging(out)
	if info.Label != "Nightmare" || info.Version != "10.1.02" {
		t.Errorf("got %+v", info)
	}
}

func TestSplitPackageLine(t *testing.T) {
	cases := []struct {
		in                  string
		apk, pkg, installer string
	}{
		{"/data/app/~~abc==/com.foo-xyz==/base.apk=com.foo  installer=null", "/data/app/~~abc==/com.foo-xyz==/base.apk", "com.foo", "null"},
		{"com.foo  installer=com.android.vending", "", "com.foo", "com.android.vending"},
		{"com.foo", "", "com.foo", ""},
		{"/system/priv-app/Bar/Bar.apk=com.bar", "/system/priv-app/Bar/Bar.apk", "com.bar", ""},
	}
	for _, c := range cases {
		apk, pkg, inst := splitPackageLine(c.in)
		if apk != c.apk || pkg != c.pkg || inst != c.installer {
			t.Errorf("%q -> (%q, %q, %q), want (%q, %q, %q)", c.in, apk, pkg, inst, c.apk, c.pkg, c.installer)
		}
	}
}
