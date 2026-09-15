package redact

import "net/url"

func URL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return "<invalid>"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}
