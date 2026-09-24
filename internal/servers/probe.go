package servers

import (
	"context"
	"html"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	probeTimeout = 1500 * time.Millisecond
	// Enough to reach the <title> of any real page without downloading a
	// whole bundle from a server that answers / with one.
	maxProbeBody  = 64 << 10
	maxTitleRunes = 80
)

var titlePattern = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// Probe reports whether a loopback port answers HTTP and, for an HTML page,
// its title.
//
// Only servers that speak HTTP are worth offering: a phone cannot do anything
// with a database or a language server, and those listen on ports agents open
// just as often as web servers do.
func Probe(ctx context.Context, port int) (title string, ok bool) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	client := &http.Client{
		Transport: &http.Transport{
			DialContext:       dialLoopback,
			DisableKeepAlives: true,
		},
		// A redirect still proves the port speaks HTTP; following it could
		// lead anywhere.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://localhost:"+strconv.Itoa(port)+"/", nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("Accept", "text/html,*/*")
	req.Header.Set("User-Agent", "agentman")
	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()

	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxProbeBody))
		title = pageTitle(body)
	}
	return title, true
}

func pageTitle(body []byte) string {
	match := titlePattern.FindSubmatch(body)
	if match == nil {
		return ""
	}
	title := strings.Join(strings.Fields(html.UnescapeString(string(match[1]))), " ")
	if !utf8.ValidString(title) {
		return ""
	}
	if utf8.RuneCountInString(title) > maxTitleRunes {
		runes := []rune(title)
		title = strings.TrimSpace(string(runes[:maxTitleRunes-1])) + "…"
	}
	return title
}

// dialLoopback reaches the port on this machine only, trying both address
// families because dev servers disagree about which one "localhost" means.
func dialLoopback(ctx context.Context, _, address string) (net.Conn, error) {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	var dialer net.Dialer
	var firstErr error
	for _, host := range []string{"127.0.0.1", "::1"} {
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
		if err == nil {
			return conn, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}
