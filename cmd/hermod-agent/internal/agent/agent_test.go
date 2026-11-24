// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package agent

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/cmd/hermod-agent/internal/detect"
	pb "github.com/gsoultan/hermod/pkg/agent/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// server is a Hermod stand-in: it sends settings, keeps records and acks.
type server struct {
	pb.UnimplementedAgentServiceServer
	settings *pb.DeviceSettingsList

	mu      sync.Mutex
	records []*pb.Record
	devices []*pb.Device
}

func (s *server) Connect(st grpc.BidiStreamingServer[pb.ConnectRequest, pb.ConnectResponse]) error {
	if _, err := st.Recv(); err != nil {
		return err
	}
	_ = st.Send(&pb.ConnectResponse{Body: &pb.ConnectResponse_Welcome{Welcome: &pb.Welcome{AgentId: "a1"}}})
	if s.settings != nil {
		_ = st.Send(&pb.ConnectResponse{Body: &pb.ConnectResponse_Settings{Settings: s.settings}})
	}
	for {
		m, err := st.Recv()
		if err != nil {
			return err
		}
		s.mu.Lock()
		if d := m.GetDevices(); d != nil {
			s.devices = d.GetDevices()
		}
		recs := m.GetRecords().GetRecords()
		s.records = append(s.records, recs...)
		s.mu.Unlock()
		if len(recs) > 0 {
			_ = st.Send(&pb.ConnectResponse{Body: &pb.ConnectResponse_Ack{Ack: &pb.Ack{UpToSeq: recs[len(recs)-1].GetSeq()}}})
		}
	}
}

func (s *server) snapshot() ([]*pb.Record, []*pb.Device) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*pb.Record(nil), s.records...), append([]*pb.Device(nil), s.devices...)
}

func serve(t *testing.T, s *server) func(context.Context, string) (net.Conn, error) {
	t.Helper()
	ln := bufconn.Listen(1 << 20)
	g := grpc.NewServer()
	pb.RegisterAgentServiceServer(g, s)
	go func() { _ = g.Serve(ln) }()
	t.Cleanup(g.Stop)
	return func(ctx context.Context, _ string) (net.Conn, error) { return ln.DialContext(ctx) }
}

func options(dir string, dial func(context.Context, string) (net.Conn, error)) Options {
	return Options{
		DataDir:      dir,
		Server:       "passthrough:///hermod",
		Token:        "t",
		Creds:        insecure.NewCredentials(),
		Version:      "test",
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		ScanInterval: 10 * time.Millisecond,
		Lister: func() ([]detect.Port, error) {
			return []detect.Port{{Name: "/dev/ttyUSB0", IsUSB: true, VID: "10c4", PID: "ea60", SerialNumber: "S1"}}, nil
		},
		Dialer:     dial,
		MinBackoff: time.Millisecond,
		MaxBackoff: 10 * time.Millisecond,
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for " + what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A scale on the LAN, claimed in Hermod: the agent gets its settings from the
// server, connects to it, and its readings arrive at the server tagged with
// the device and in order.
func TestReadingsFromAClaimedTCPDeviceReachTheServer(t *testing.T) {
	scale := listen(t)
	defer scale.Close()
	go func() {
		conn, err := scale.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("ST,GS,+0012.40kg\r\nST,GS,+0012.38kg\r\n"))
		time.Sleep(time.Second)
	}()

	srv := &server{settings: &pb.DeviceSettingsList{Devices: []*pb.DeviceSettings{{
		DeviceId: "tcp-scale", Kind: pb.DeviceKind_DEVICE_KIND_TCP, Enabled: true,
		Tcp: &pb.TcpSettings{Address: scale.Addr().String()},
	}}}}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, options(t.TempDir(), serve(t, srv))) }()

	eventually(t, "two records", func() bool { r, _ := srv.snapshot(); return len(r) == 2 })
	recs, devs := srv.snapshot()
	if string(recs[0].GetRaw()) != "ST,GS,+0012.40kg" || recs[1].GetSeq() != recs[0].GetSeq()+1 || recs[0].GetDeviceId() != "tcp-scale" {
		t.Fatalf("records = %v", recs)
	}
	eventually(t, "the serial port and the TCP device in the device list", func() bool {
		_, devs = srv.snapshot()
		return len(devs) == 2
	})
	ids := map[string]bool{}
	for _, d := range devs {
		ids[d.GetDeviceId()] = true
	}
	if !ids["usb:10c4:ea60:S1"] || !ids["tcp-scale"] {
		t.Fatalf("devices = %v", devs)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// Settings are cached, so after a restart the agent reads its devices even
// with the server unreachable, and those readings wait in the spool.
func TestCachedSettingsKeepDevicesReadingWithoutTheServer(t *testing.T) {
	dir := t.TempDir()
	device := listen(t)
	defer device.Close()
	go func() {
		for {
			conn, err := device.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("reading\n"))
			_ = conn.Close()
		}
	}()
	list := &pb.DeviceSettingsList{Devices: []*pb.DeviceSettings{{
		DeviceId: "d", Kind: pb.DeviceKind_DEVICE_KIND_TCP, Enabled: true,
		Tcp: &pb.TcpSettings{Address: device.Addr().String()},
	}}}
	if err := saveSettings(dir, list); err != nil {
		t.Fatal(err)
	}
	unreachable := func(context.Context, string) (net.Conn, error) { return nil, io.ErrClosedPipe }
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, options(dir, unreachable)) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-done

	srv := &server{}
	ctx, cancel = context.WithCancel(t.Context())
	done = make(chan error, 1)
	go func() { done <- Run(ctx, options(dir, serve(t, srv))) }()
	// Wait for the agent before the data directory is removed.
	t.Cleanup(func() { cancel(); <-done })
	eventually(t, "spooled readings delivered", func() bool { r, _ := srv.snapshot(); return len(r) > 0 })
	recs, _ := srv.snapshot()
	if string(recs[0].GetRaw()) != "reading" || recs[0].GetSeq() != 1 {
		t.Fatalf("first record = %v", recs[0])
	}
}

func TestInvalidSettingsAreSkippedNotFatal(t *testing.T) {
	bad := []*pb.DeviceSettings{
		{DeviceId: "", Kind: pb.DeviceKind_DEVICE_KIND_SERIAL, Serial: &pb.SerialSettings{BaudRate: 9600}},
		{DeviceId: "s", Kind: pb.DeviceKind_DEVICE_KIND_SERIAL},
		{DeviceId: "s", Kind: pb.DeviceKind_DEVICE_KIND_SERIAL, Serial: &pb.SerialSettings{BaudRate: 9600, DataBits: 9}},
		{DeviceId: "t", Kind: pb.DeviceKind_DEVICE_KIND_TCP, Tcp: &pb.TcpSettings{Address: ":9100", Mode: pb.TcpMode_TCP_MODE_LISTEN}},
		{DeviceId: "t", Kind: pb.DeviceKind_DEVICE_KIND_TCP, Tcp: &pb.TcpSettings{Address: "no-port"}},
		{DeviceId: "f", Kind: pb.DeviceKind_DEVICE_KIND_TCP, Tcp: &pb.TcpSettings{Address: "h:1"}, Framing: &pb.Framing{Kind: pb.FramingKind_FRAMING_KIND_DELIMITER}},
		{DeviceId: "k", Kind: pb.DeviceKind(42)},
	}
	for _, s := range bad {
		if err := validate(s); err == nil {
			t.Errorf("validate(%v) accepted", s)
		}
	}
	good := &pb.DeviceSettings{DeviceId: "ok", Kind: pb.DeviceKind_DEVICE_KIND_SERIAL, Serial: &pb.SerialSettings{BaudRate: 9600}}
	if err := validate(good); err != nil {
		t.Fatalf("validate(good) = %v", err)
	}
}

// Changed settings restart a reader; unchanged ones leave it running.
func TestApplyRestartsOnlyChangedReaders(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := newReaders(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), func(string, []byte, string, time.Time) {},
		func(string) (string, bool) { return "", false }, nil, nil)
	defer r.stopAll()
	s := func(baud uint32) *pb.DeviceSettings {
		return &pb.DeviceSettings{DeviceId: "x", Kind: pb.DeviceKind_DEVICE_KIND_SERIAL, Enabled: true, Serial: &pb.SerialSettings{BaudRate: baud}}
	}
	r.apply(&pb.DeviceSettingsList{Devices: []*pb.DeviceSettings{s(9600)}})
	first := r.running["x"]
	r.apply(&pb.DeviceSettingsList{Devices: []*pb.DeviceSettings{s(9600)}})
	if r.running["x"] != first {
		t.Fatal("unchanged settings restarted the reader")
	}
	r.apply(&pb.DeviceSettingsList{Devices: []*pb.DeviceSettings{s(19200)}})
	if r.running["x"] == first {
		t.Fatal("changed settings kept the old reader")
	}
	r.apply(&pb.DeviceSettingsList{})
	if len(r.running) != 0 {
		t.Fatal("a device missing from the settings is still read")
	}
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}
