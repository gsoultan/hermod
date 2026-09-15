package evaluator

import (
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// TestADeleteResolvesItsOwnKeyNotTheLSN pins which "id" a CDC delete resolves to.
//
// pkg/comm/source/postgres builds a delete with SetID(lsn) and a before-image
// and no after-image (postgres.go:1259-1284), so the row's own columns exist
// only in the before-image. GetMsgValByPath consults the "id" virtual field --
// which returns msg.ID(), the LSN -- before it reaches the before-image, so a
// sink mapping keyed on "id" binds the LSN and its DELETE matches no row.
func TestADeleteResolvesItsOwnKeyNotTheLSN(t *testing.T) {
	msg := message.AcquireMessage()
	msg.SetID("0/2604B18") // what the CDC source sets: the LSN
	msg.SetOperation(hermod.OpDelete)
	msg.SetTable("orders")
	msg.SetBefore([]byte(`{"id":"M-1","customer_id":"C-1","amount":"20.75"}`))

	// A transformation has run, so data holds its output and nothing else.
	msg.SetData("customer_name", "ACME Corp")

	if got := GetMsgValByPath(msg, "id"); got != "M-1" {
		t.Errorf(`GetMsgValByPath(delete, "id") = %v, want "M-1"; a sink keyed on `+
			`this column deletes WHERE key = %v and removes nothing`, got, got)
	}

	// The other columns already come from the before-image; they are the control.
	if got := GetMsgValByPath(msg, "customer_id"); got != "C-1" {
		t.Errorf(`GetMsgValByPath(delete, "customer_id") = %v, want "C-1"`, got)
	}
}
