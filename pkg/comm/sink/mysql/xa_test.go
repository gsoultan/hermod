package mysql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/gsoultan/hermod"
)

// ---------------------------------------------------------------------------
// MySQL XA participation in a transactional sink group.
//
// XA statements take no bind parameters, so the identifier is interpolated —
// which makes validating it a security property, not tidiness. These tests pin
// the identifier format, the parsing of XA RECOVER rows, the server-version
// gate, and the error paths that must refuse before anything reaches the
// server. Behaviour against a real server is in xa_integration_test.go.
// ---------------------------------------------------------------------------

const validTxID = "6f1c2a8e-3b4d-4c5e-9f60-718293a4b5c6"

// The sink must satisfy the interfaces the coordinator and the group look for;
// a missing one is a group that refuses to start, or a participant named too late.
func TestMySQLSinkIsATwoPhaseParticipant(t *testing.T) {
	var s any = NewMySQLSink("u:p@tcp(127.0.0.1:1)/d", "t", nil, false, "", "", "", "", false, false)
	if _, ok := s.(hermod.TwoPhaseCommit); !ok {
		t.Error("MySQLSink does not implement hermod.TwoPhaseCommit; a transactional group would refuse it")
	}
	if _, ok := s.(hermod.NamedBegin); !ok {
		t.Error("MySQLSink does not implement hermod.NamedBegin; XA START needs the coordinator's ID " +
			"at Begin, and without it the sink would have to name the transaction itself")
	}
	if _, ok := s.(hermod.TwoPhaseCommitPreflight); !ok {
		t.Error("MySQLSink does not implement hermod.TwoPhaseCommitPreflight; an unsuitable server " +
			"would surface as a mid-batch failure instead of a refusal at start-up")
	}
}

func TestXIDLiteral(t *testing.T) {
	got, err := xidLiteral(validTxID)
	if err != nil {
		t.Fatalf("xidLiteral(%q): %v", validTxID, err)
	}
	want := fmt.Sprintf("'%s','',%d", validTxID, xaFormatID)
	if got != want {
		t.Errorf("xidLiteral = %s, want %s", got, want)
	}
}

func TestXIDLiteralRefusesAnythingButACanonicalUUID(t *testing.T) {
	for _, bad := range []string{
		"",
		"not-a-uuid",
		"x'); DROP TABLE t; --",
		validTxID + "'",
		"'" + validTxID,
		// uuid.Parse accepts these forms; they are refused anyway, because the
		// identifier recorded in the log must be byte-for-byte the one in force.
		"urn:uuid:" + validTxID,
		"{" + validTxID + "}",
		strings.ReplaceAll(validTxID, "-", ""),
		strings.ToUpper(validTxID),
	} {
		if lit, err := xidLiteral(bad); err == nil {
			t.Errorf("xidLiteral(%q) = %s, want a refusal: it would be interpolated into an XA statement", bad, lit)
		}
	}
}

func TestParseRecoveredXID(t *testing.T) {
	data := []byte(validTxID)
	cases := []struct {
		name     string
		formatID int64
		gtridLen int64
		bqualLen int64
		data     []byte
		want     string
		ours     bool
	}{
		{"ours", xaFormatID, 36, 0, data, validTxID, true},
		{"another application's format", 1, 36, 0, data, "", false},
		{"has a branch qualifier", xaFormatID, 36, 3, append(append([]byte{}, data...), "abc"...), "", false},
		{"gtrid not a uuid", xaFormatID, 5, 0, []byte("hello"), "", false},
		{"length beyond data", xaFormatID, 40, 0, data, "", false},
		{"negative length", xaFormatID, -1, 0, data, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ours := parseRecoveredXID(tc.formatID, tc.gtridLen, tc.bqualLen, tc.data)
			if ours != tc.ours || got != tc.want {
				t.Errorf("parseRecoveredXID = (%q, %v), want (%q, %v)", got, ours, tc.want, tc.ours)
			}
		})
	}
}

func TestCheckXAServerVersion(t *testing.T) {
	cases := []struct {
		version         string
		ok              bool
		checkSupportVar bool
	}{
		{"8.4.3", true, false},
		{"8.0.40-0ubuntu0.22.04.1", true, false},
		{"5.7.44-log", true, false},
		{"5.7.10", true, false},
		{"5.7.9", true, true},
		{"5.7.7", true, true},
		{"5.7.6", false, false},
		{"5.6.51", false, false},
		{"10.11.6-MariaDB", true, false},
		{"10.5.2-MariaDB-1:10.5.2+maria~focal", true, false},
		{"5.5.5-10.6.12-MariaDB", true, false}, // MariaDB's replication-compatible version prefix
		{"10.5.1-MariaDB", false, false},
		{"10.4.32-MariaDB", false, false},
		{"5.5.5-10.4.32-MariaDB", false, false},
		{"garbage", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.version, func(t *testing.T) {
			checkVar, err := checkXAServerVersion(tc.version)
			if (err == nil) != tc.ok {
				t.Fatalf("checkXAServerVersion(%q) err = %v, want ok=%v", tc.version, err, tc.ok)
			}
			if checkVar != tc.checkSupportVar {
				t.Errorf("checkXAServerVersion(%q) asks for innodb_support_xa = %v, want %v",
					tc.version, checkVar, tc.checkSupportVar)
			}
		})
	}
}

func TestIsXAUnknown(t *testing.T) {
	if !isXAUnknown(&gomysql.MySQLError{Number: 1397, Message: "XAER_NOTA: Unknown XID"}) {
		t.Error("error 1397 (XAER_NOTA) is MySQL saying the XID does not exist")
	}
	if !isXAUnknown(fmt.Errorf("wrapped: %w", &gomysql.MySQLError{Number: 1397})) {
		t.Error("a wrapped 1397 must still be recognised")
	}
	if isXAUnknown(&gomysql.MySQLError{Number: 1399}) {
		t.Error("1399 (XAER_RMFAIL) is a state error, not a missing XID")
	}
	if isXAUnknown(errors.New("Unknown XID")) || isXAUnknown(nil) {
		t.Error("only the server's error number counts, not message text")
	}
}

// The error paths below must refuse before dialing: the DSN points nowhere, so
// reaching the network would show up as a connection error instead.
func unreachableSink() *MySQLSink {
	return NewMySQLSink("u:p@tcp(127.0.0.1:1)/d?timeout=200ms", "t", nil, false, "", "", "", "", false, false)
}

func TestPrepareWithoutABegunTransactionIsRefused(t *testing.T) {
	_, err := unreachableSink().Prepare(context.Background(), validTxID)
	if err == nil || !strings.Contains(err.Error(), "no active transaction") {
		t.Fatalf("Prepare without Begin = %v, want \"no active transaction\"", err)
	}
}

func TestBeginNamedRefusesAnUnsafeID(t *testing.T) {
	err := unreachableSink().BeginNamed(context.Background(), "x'); DROP TABLE t; --")
	if err == nil || !strings.Contains(err.Error(), "UUID") {
		t.Fatalf("BeginNamed with a hostile ID = %v, want a refusal naming the UUID requirement", err)
	}
}

func TestResolvingRefusesAnUnsafeID(t *testing.T) {
	s := unreachableSink()
	for name, f := range map[string]func(context.Context, string) error{
		"CommitPrepared":   s.CommitPrepared,
		"RollbackPrepared": s.RollbackPrepared,
	} {
		err := f(context.Background(), "x'); DROP TABLE t; --")
		if err == nil || !strings.Contains(err.Error(), "UUID") {
			t.Errorf("%s with a hostile ID = %v, want a refusal naming the UUID requirement", name, err)
		}
	}
}

// A transaction opened under one name cannot be prepared under another: an XA
// transaction cannot be renamed, so honouring the request would mean the log
// records a name that nothing on the server carries.
func TestPrepareUnderADifferentIDThanItBeganWithIsRefused(t *testing.T) {
	s := unreachableSink()
	s.setXA(&xaTxn{xid: validTxID})
	other := "11111111-2222-4333-8444-555555555555"
	_, err := s.Prepare(context.Background(), other)
	if err == nil || !strings.Contains(err.Error(), "cannot be renamed") {
		t.Fatalf("Prepare(%q) after beginning under %q = %v, want a refusal", other, validTxID, err)
	}
}

// Rollback with nothing open is a no-op, as on PostgreSQL: the coordinator rolls
// back every participant on an early failure, including ones never begun.
func TestRollbackWithNothingOpenIsANoOp(t *testing.T) {
	if err := unreachableSink().Rollback(context.Background()); err != nil {
		t.Fatalf("Rollback with nothing open = %v, want nil", err)
	}
}

func TestCommitWithNothingOpenIsAnError(t *testing.T) {
	err := unreachableSink().Commit(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no active transaction") {
		t.Fatalf("Commit with nothing open = %v, want \"no active transaction\"", err)
	}
}
