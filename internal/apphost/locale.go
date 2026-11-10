package apphost

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Locale is the user's own region for dates and times, as a BCP 47 tag the
// page's Intl takes ("en-NG", "en-GB-u-hc-h23"). A web view formats with
// its language alone (en-US, 3:32 PM), where the Mac app follows the
// region and the 24-hour switch in System Settings (15:32).
func Locale() string {
	if runtime.GOOS == "darwin" {
		locale := readDefault("AppleLocale")
		return localeTag(locale, readDefault("AppleICUForce24HourTime"), readDefault("AppleICUForce12HourTime"))
	}
	for _, name := range []string{"LC_ALL", "LC_TIME", "LANG"} {
		if v := os.Getenv(name); v != "" && v != "C" && v != "POSIX" {
			return localeTag(v, "", "")
		}
	}
	return ""
}

func readDefault(key string) string {
	out, err := exec.Command("defaults", "read", "-g", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// localeTag turns a system locale (en_US@rg=ngzzzz, en_GB.UTF-8) into a
// BCP 47 tag, with the region a "rg" keyword names in place of the
// language's, and the hour cycle the user forced, if any.
func localeTag(locale, force24, force12 string) string {
	locale = strings.TrimSpace(locale)
	if locale == "" {
		return ""
	}
	base, keywords, _ := strings.Cut(locale, "@")
	base, _, _ = strings.Cut(base, ".")
	lang, region, _ := strings.Cut(base, "_")
	for _, kw := range strings.Split(keywords, ";") {
		if k, v, ok := strings.Cut(kw, "="); ok && k == "rg" && len(v) >= 2 {
			region = strings.ToUpper(v[:2])
		}
	}
	if lang == "" {
		return ""
	}
	tag := strings.ToLower(lang)
	if region != "" {
		tag += "-" + strings.ToUpper(region)
	}
	switch {
	case force24 == "1" || force24 == "true":
		tag += "-u-hc-h23"
	case force12 == "1" || force12 == "true":
		tag += "-u-hc-h12"
	}
	return tag
}
