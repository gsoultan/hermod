package registry

import (
	"testing"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
)

func TestRecordAndLoadDeliveredSample(t *testing.T) {
	tests := []struct {
		name      string
		sourceID  string
		store     func() hermod.Message
		loadID    string
		wantFound bool
		wantData  string
	}{
		{
			name:      "stored message is returned for matching source id",
			sourceID:  "src-stored",
			store:     func() hermod.Message { m := message.AcquireMessage(); m.SetData("k", "v"); return m },
			loadID:    "src-stored",
			wantFound: true,
			wantData:  "v",
		},
		{
			name:      "unknown source id returns not found",
			sourceID:  "src-known",
			store:     func() hermod.Message { m := message.AcquireMessage(); m.SetData("k", "v"); return m },
			loadID:    "src-other",
			wantFound: false,
		},
		{
			name:      "empty source id is ignored on store",
			sourceID:  "",
			store:     func() hermod.Message { m := message.AcquireMessage(); m.SetData("k", "v"); return m },
			loadID:    "",
			wantFound: false,
		},
		{
			name:      "nil message is ignored on store",
			sourceID:  "src-nil",
			store:     func() hermod.Message { return nil },
			loadID:    "src-nil",
			wantFound: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recordDeliveredSample(tc.sourceID, tc.store())

			got, ok := loadDeliveredSample(tc.loadID)
			if ok != tc.wantFound {
				t.Fatalf("loadDeliveredSample found = %v; want %v", ok, tc.wantFound)
			}
			if !tc.wantFound {
				return
			}
			if v := got.Data()["k"]; v != tc.wantData {
				t.Errorf("data[k] = %v; want %v", v, tc.wantData)
			}
		})
	}
}

func TestRecordDeliveredSampleStoresClone(t *testing.T) {
	const id = "src-clone"
	original := message.AcquireMessage()
	original.SetData("count", 1)

	recordDeliveredSample(id, original)

	// Mutating the original after storing must not affect the cached copy.
	original.SetData("count", 99)

	got, ok := loadDeliveredSample(id)
	if !ok {
		t.Fatalf("expected cached sample for %q", id)
	}
	if v := got.Data()["count"]; v != 1 {
		t.Errorf("cached data[count] = %v; want 1 (clone should be isolated)", v)
	}
}

func TestHasSampleData(t *testing.T) {
	tests := []struct {
		name string
		msg  func() hermod.Message
		want bool
	}{
		{name: "nil message", msg: func() hermod.Message { return nil }, want: false},
		{name: "empty message", msg: func() hermod.Message { return message.AcquireMessage() }, want: false},
		{
			name: "message with data field",
			msg:  func() hermod.Message { m := message.AcquireMessage(); m.SetData("a", 1); return m },
			want: true,
		},
		{
			name: "message with payload",
			msg:  func() hermod.Message { m := message.AcquireMessage(); m.SetPayload([]byte("raw")); return m },
			want: true,
		},
		{
			name: "message with after body",
			msg:  func() hermod.Message { m := message.AcquireMessage(); m.SetAfter([]byte("after")); return m },
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasSampleData(tc.msg()); got != tc.want {
				t.Errorf("hasSampleData = %v; want %v", got, tc.want)
			}
		})
	}
}
