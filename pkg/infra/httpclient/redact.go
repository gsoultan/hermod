package httpclient

import (
	"errors"
	"net/url"
	"strings"
)

// redactedMarker stands in for the part of a URL that was removed. It is the
// marker SanitizeDBError uses for the same job.
const redactedMarker = "[redacted]"

// RedactURLError keeps a request's credentials out of the error it failed with.
//
// net/http reports a failed request as a *url.Error whose text quotes the whole
// request URL — `Post "https://api.telegram.org/bot<TOKEN>/sendMessage": dial
// tcp: ...` — and it removes a userinfo password from it and nothing else.
// Connectors that authenticate through the URL returned that error unchanged:
// Telegram's token is part of the path, the Graph API takes access_token in the
// query, and a Slack or Discord webhook URL is the credential itself. The error
// then went where errors go — the log table, the workflow's status, and the
// "Workflow Error" alert, which is sent to every notification channel — on any
// network failure at all.
//
// The URL is cut to its scheme and host: which server could not be reached is
// what a transport failure is diagnosed by, and nothing after the host is
// needed for that. The error keeps its Op and its cause, so errors.Is and
// Timeout() answer as they did. url.Parse failures are *url.Error too, which
// matters because a token pasted with its trailing newline fails parsing, and
// the parse error quotes the URL.
//
// Give it the error net/http returned. Anything that wrapped that error has
// already copied the URL into its own text, so a wrapped error comes back as
// the redacted *url.Error alone, without the wrapper's words. Errors that are
// not about a URL are returned unchanged.
func RedactURLError(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	return &url.Error{Op: ue.Op, URL: redactURL(ue.URL), Err: ue.Err}
}

// redactURL keeps a URL's scheme and host and drops everything a credential can
// travel in: userinfo, path, query and fragment. It reads the string directly
// rather than through url.Parse, because a URL that failed to parse is one of
// the things it is given.
func redactURL(raw string) string {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		// Without a scheme there is no telling where the host ends.
		return redactedMarker
	}

	authority, tail := rest, ""
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		authority, tail = rest[:i], rest[i:]
	}
	hadUserinfo := false
	if i := strings.LastIndexByte(authority, '@'); i >= 0 {
		authority, hadUserinfo = authority[i+1:], true
	}

	if tail == "" && !hadUserinfo {
		return raw
	}
	return scheme + "://" + authority + "/" + redactedMarker
}
