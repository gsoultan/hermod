package fcm

import (
	"errors"
	"testing"

	"github.com/gsoultan/hermod"
)

// The FCM sink classified its refusals for a caller "that can tell the
// difference", and no caller could: ErrPermanent was this package's own value,
// so the engine retried a refusal that said it could never succeed. Every
// permanent FCM error has to read as hermod.ErrPermanent to anything above it.
func TestPermanentRefusalsReadAsPermanentToTheEngine(t *testing.T) {
	cases := map[string]error{
		"a refusal built here":   permanentf("fcm sink: message %s would be empty", "m-1"),
		"a wrapped refusal":      permanent(errors.New("invalid argument")),
		"an unregistered token":  &UnregisteredTokenError{Token: "tok", Err: errors.New("gone")},
		"the package's own mark": ErrPermanent,
	}
	for name, err := range cases {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(err, hermod.ErrPermanent) {
				t.Errorf("%v does not read as hermod.ErrPermanent", err)
			}
			if !errors.Is(err, ErrPermanent) {
				t.Errorf("%v no longer reads as this package's ErrPermanent", err)
			}
		})
	}

	if errors.Is(classify(errors.New("connection reset"), ""), hermod.ErrPermanent) {
		t.Error("a dropped connection was marked permanent; it says nothing about the message")
	}
}
