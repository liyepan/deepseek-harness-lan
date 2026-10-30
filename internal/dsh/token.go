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
