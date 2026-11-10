package main

import "testing"

func TestLocaleTag(t *testing.T) {
	for _, c := range []struct{ locale, force24, force12, want string }{
		{"en_US@rg=ngzzzz", "", "", "en-NG"},
		{"en_US", "", "", "en-US"},
		{"en_GB.UTF-8", "", "", "en-GB"},
		{"de_DE@currency=EUR;rg=atzzzz", "", "", "de-AT"},
		{"en_US", "1", "", "en-US-u-hc-h23"},
		{"en_GB", "", "1", "en-GB-u-hc-h12"},
		{"fr", "", "", "fr"},
		{"", "1", "", ""},
	} {
		if got := localeTag(c.locale, c.force24, c.force12); got != c.want {
			t.Errorf("localeTag(%q, %q, %q) = %q, want %q", c.locale, c.force24, c.force12, got, c.want)
		}
	}
}
