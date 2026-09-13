package fcm

import (
	"errors"
	"fmt"

	"firebase.google.com/go/v4/messaging"
)

// ErrPermanent marks a failure that retrying cannot fix: a dead registration
// token, a payload over FCM's size limit, credentials for the wrong project.
//
// Hermod's RetrySink treats every error alike, so without this distinction a
// message addressed to a device that was uninstalled months ago burns the full
// retry budget on every attempt before reaching the dead-letter sink. Callers
// that can tell the difference should check errors.Is(err, ErrPermanent) and
// dead-letter immediately.
var ErrPermanent = errors.New("fcm: permanent failure")

// UnregisteredTokenError names a registration token FCM says no longer exists.
//
// It is worth its own type because it is actionable in a way other refusals are
// not: the row that supplied the token should be deleted, and nothing else in
// the pipeline will ever say so.
type UnregisteredTokenError struct {
	Token string
	Err   error
}

func (e *UnregisteredTokenError) Error() string {
	return fmt.Sprintf("fcm: registration token %q is no longer registered; the device should be removed from the source: %v", e.Token, e.Err)
}

func (e *UnregisteredTokenError) Unwrap() error { return e.Err }

// Is makes errors.Is(err, ErrPermanent) true for a dead token without the
// caller having to know this type exists.
func (e *UnregisteredTokenError) Is(target error) bool { return target == ErrPermanent }

// permanent wraps err so errors.Is(err, ErrPermanent) holds, preserving the
// original for errors.As and for the message the operator reads.
func permanent(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrPermanent) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrPermanent, err)
}

// permanentf builds a permanent error from a format string.
func permanentf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrPermanent, fmt.Sprintf(format, args...))
}

// classify turns an FCM refusal into an error the retry machinery can act on.
//
// The split follows what FCM itself says is worth another attempt. UNAVAILABLE,
// INTERNAL and the two rate limits are the service asking for patience.
// Everything FCM calls a client error is a statement about this message or
// these credentials, and sending it again unchanged produces the same answer.
//
// An error that is not an FCM refusal at all — a dropped connection, a DNS
// failure — is left retryable: it says nothing about the message.
func classify(err error, token string) error {
	if err == nil {
		return nil
	}

	if messaging.IsUnregistered(err) {
		return &UnregisteredTokenError{Token: token, Err: err}
	}

	switch {
	case messaging.IsInvalidArgument(err),
		messaging.IsSenderIDMismatch(err),
		messaging.IsThirdPartyAuthError(err):
		return permanent(err)
	}

	return err
}

// reachedFCM reports whether an error proves the round trip to FCM completed.
//
// Ping needs this and nothing else needs it: a refusal about the *message*
// means the credentials worked and the service answered, which is exactly what
// a connection test is asking about. A refusal about the *credentials* is the
// failure Ping exists to surface.
func reachedFCM(err error) bool {
	switch {
	case messaging.IsThirdPartyAuthError(err), messaging.IsSenderIDMismatch(err):
		return false
	case messaging.IsInvalidArgument(err), messaging.IsUnregistered(err):
		return true
	}
	return false
}
