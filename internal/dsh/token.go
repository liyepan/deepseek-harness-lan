package dsh

import (
	"net/url"
	"regexp"
)

// TokenQuery is the query parameter "dsh web" uses for its one-time browser
// authentication exchange.
const TokenQuery = "token"

// urlLine matches the startup line "dsh web" prints once the server is bound:
//
//	dsh web: http://127.0.0.1:3080/?token=XXXX (LAN: http://192.168.1.5:3080/?token=XXXX)
var urlLine = regexp.MustCompile(`dsh web:\s+(https?://\S+)`)

// LaunchURL returns the first URL of a dsh startup line, or "" when the line
// is not that line.
func LaunchURL(line string) string {
	m := urlLine.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	return m[1]
}

// RedactLaunchLine replaces the token-bearing form of a dsh startup line with
// a plain address line, reporting whether it changed anything. dsh prints its
// token exactly once; this tool captures it, and the terminal does not need to
// keep a copy.
func RedactLaunchLine(line string) (string, bool) {
	raw := LaunchURL(line)
	if raw == "" || TokenFromLine(line) == "" {
		return line, false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "dsh web: (startup URL hidden)", true
	}
	u.RawQuery = ""
	u.Fragment = ""
	return "dsh web: " + u.String(), true
}

// TokenFromLine extracts the launch token from a dsh startup line.
func TokenFromLine(line string) string {
	raw := LaunchURL(line)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Query().Get(TokenQuery)
}
