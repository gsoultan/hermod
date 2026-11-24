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
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"

	pb "github.com/gsoultan/hermod/pkg/agent/proto"
	"golang.org/x/net/dns/dnsmessage"
)

// announcer answers an mDNS query for any service with one instance.
func announcer(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	pc, err := lc.ListenPacket(t.Context(), "udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			var p dnsmessage.Parser
			h, err := p.Start(buf[:n])
			if err != nil {
				continue
			}
			q, err := p.Question()
			if err != nil {
				continue
			}
			inst := dnsmessage.MustNewName("scale-1." + q.Name.String())
			host := dnsmessage.MustNewName("esp32.local.")
			rh := func(n dnsmessage.Name) dnsmessage.ResourceHeader {
				return dnsmessage.ResourceHeader{Name: n, Class: dnsmessage.ClassINET, TTL: 120}
			}
			b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true})
			_ = b.StartAnswers()
			_ = b.PTRResource(rh(q.Name), dnsmessage.PTRResource{PTR: inst})
			_ = b.SRVResource(rh(inst), dnsmessage.SRVResource{Target: host, Port: 9100})
			_ = b.AResource(rh(host), dnsmessage.AResource{A: [4]byte{192, 168, 1, 40}})
			out, _ := b.Finish()
			_, _ = pc.WriteTo(out, from)
		}
	}()
	return pc.LocalAddr().String()
}

// What the agent finds by itself is reported for a user to add: a device
// announcing itself over mDNS, and a Modbus port found by the opt-in scan.
func TestDiscoveredDevicesAreReported(t *testing.T) {
	plc := listen(t)
	defer plc.Close()
	go func() {
		for {
			c, err := plc.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	port := uint16(plc.Addr().(*net.TCPAddr).Port)
	srv := &server{}
	opt := options(t.TempDir(), serve(t, srv))
	opt.MDNSServices = []string{"_hermod._tcp"}
	opt.MDNSAddress = announcer(t)
	opt.ScanSubnets = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	opt.ScanPort = port
	opt.DiscoverInterval = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opt) }()
	t.Cleanup(func() { cancel(); <-done })

	want := map[string]func(*pb.Device) bool{
		"mdns:scale-1._hermod._tcp": func(d *pb.Device) bool {
			return d.GetDiscoveredBy() == "mdns" && d.GetService() == "_hermod._tcp" && d.GetPort() == "192.168.1.40:9100" &&
				d.GetKind() == pb.DeviceKind_DEVICE_KIND_TCP && d.GetProduct() == "scale-1" && d.GetPresent()
		},
		fmt.Sprintf("modbus:127.0.0.1:%d", port): func(d *pb.Device) bool {
			return d.GetDiscoveredBy() == "scan" && d.GetKind() == pb.DeviceKind_DEVICE_KIND_MODBUS && d.GetPresent()
		},
	}
	eventually(t, "both discovered devices", func() bool {
		_, devs := srv.snapshot()
		ok := 0
		for _, d := range devs {
			if check, found := want[d.GetDeviceId()]; found && check(d) {
				ok++
			}
		}
		return ok == len(want)
	})
}
