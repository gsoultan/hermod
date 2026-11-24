// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package discover finds LAN devices the agent was not told about: devices
// announcing themselves over mDNS, and, only when the operator asks for it,
// Modbus devices found by trying port 502 across a subnet. What it finds is
// reported to Hermod for a user to add; nothing is read until they do.
package discover

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// MDNSAddress is where mDNS queries go.
const MDNSAddress = "224.0.0.251:5353"

// Found is a device that answered an mDNS query.
type Found struct {
	Instance string // "scale-1"
	Service  string // "_hermod._tcp"
	Host     string // "esp32-scale.local"
	Addr     netip.AddrPort
}

// Browse asks for the given service types (such as "_modbus._tcp") at to,
// normally MDNSAddress, and gathers the answers that arrive within wait. The
// query goes from an ephemeral port, so responders answer it directly
// (RFC 6762, section 6.7) and the agent needs no access to port 5353.
func Browse(ctx context.Context, to string, services []string, wait time.Duration) ([]Found, error) {
	dst, err := net.ResolveUDPAddr("udp4", to)
	if err != nil {
		return nil, err
	}
	q, err := query(services)
	if err != nil {
		return nil, err
	}
	var lc net.ListenConfig
	pc, err := lc.ListenPacket(ctx, "udp4", ":0")
	if err != nil {
		return nil, err
	}
	defer func() { _ = pc.Close() }()
	if _, err := pc.WriteTo(q, dst); err != nil {
		return nil, fmt.Errorf("mdns query: %w", err)
	}
	deadline := time.Now().Add(wait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = pc.SetReadDeadline(deadline)
	var recs records
	buf := make([]byte, 9000)
	for {
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			break // the deadline: done listening
		}
		recs.add(buf[:n])
	}
	return recs.found(services), nil
}

func query(services []string) ([]byte, error) {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{})
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	for _, s := range services {
		name, err := dnsmessage.NewName(s + ".local.")
		if err != nil {
			return nil, fmt.Errorf("mdns service %q: %w", s, err)
		}
		if err := b.Question(dnsmessage.Question{Name: name, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET}); err != nil {
			return nil, err
		}
	}
	return b.Finish()
}

// records are what the answers said, by name.
type records struct {
	ptr map[string][]string // service -> instances
	srv map[string]dnsmessage.SRVResource
	a   map[string]netip.Addr
}

func (r *records) add(msg []byte) {
	if r.ptr == nil {
		r.ptr, r.srv, r.a = map[string][]string{}, map[string]dnsmessage.SRVResource{}, map[string]netip.Addr{}
	}
	var p dnsmessage.Parser
	if _, err := p.Start(msg); err != nil {
		return
	}
	if err := p.SkipAllQuestions(); err != nil {
		return
	}
	for {
		h, err := p.AnswerHeader()
		if err != nil {
			// Answers done; mDNS puts SRV and A in additionals as often.
			if !errors.Is(err, dnsmessage.ErrSectionDone) {
				return
			}
			break
		}
		r.resource(&p, h)
	}
	if err := p.SkipAllAuthorities(); err != nil {
		return
	}
	for {
		h, err := p.AdditionalHeader()
		if err != nil {
			return
		}
		r.resource(&p, h)
	}
}

func (r *records) resource(p *dnsmessage.Parser, h dnsmessage.ResourceHeader) {
	name := strings.ToLower(h.Name.String())
	switch h.Type {
	case dnsmessage.TypePTR:
		if res, err := p.PTRResource(); err == nil && !slices.Contains(r.ptr[name], res.PTR.String()) {
			r.ptr[name] = append(r.ptr[name], res.PTR.String())
		}
	case dnsmessage.TypeSRV:
		if res, err := p.SRVResource(); err == nil {
			r.srv[name] = res
		}
	case dnsmessage.TypeA:
		if res, err := p.AResource(); err == nil {
			r.a[name] = netip.AddrFrom4(res.A)
		}
	default:
		_ = p.SkipAnswer()
	}
}

func (r *records) found(services []string) []Found {
	var out []Found
	for _, s := range services {
		svc := strings.ToLower(s + ".local.")
		for _, inst := range r.ptr[svc] {
			srv, ok := r.srv[strings.ToLower(inst)]
			if !ok {
				continue
			}
			addr, ok := r.a[strings.ToLower(srv.Target.String())]
			if !ok {
				continue
			}
			out = append(out, Found{
				Instance: strings.TrimSuffix(inst, "."+s+".local."),
				Service:  s,
				Host:     strings.TrimSuffix(srv.Target.String(), "."),
				Addr:     netip.AddrPortFrom(addr, srv.Port),
			})
		}
	}
	return out
}

// MaxScanHosts bounds one scan: a /20, so an opt-in scan never sweeps a
// whole private range by mistake.
const MaxScanHosts = 4096

// scanWorkers is how many connection attempts run at once.
const scanWorkers = 32

// ParsePrefixes reads a comma-separated list of IPv4 CIDR ranges or
// addresses.
func ParsePrefixes(list string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, e := range strings.Split(list, ",") {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		p, err := netip.ParsePrefix(e)
		if err != nil {
			a, aerr := netip.ParseAddr(e)
			if aerr != nil {
				return nil, fmt.Errorf("%q: not an IPv4 address or CIDR range", e)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		if !p.Addr().Is4() {
			return nil, fmt.Errorf("%q: only IPv4 ranges can be scanned", e)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

// Scan tries a TCP connection to port on every address in prefixes and
// returns those that accept, in address order. Each attempt waits at most
// timeout. More than MaxScanHosts addresses in all is refused.
func Scan(ctx context.Context, prefixes []netip.Prefix, port uint16, timeout time.Duration) ([]netip.AddrPort, error) {
	total := 0
	for _, p := range prefixes {
		if !p.Addr().Is4() {
			return nil, fmt.Errorf("%s: only IPv4 ranges can be scanned", p)
		}
		total += 1 << (32 - p.Bits())
		if total > MaxScanHosts {
			return nil, fmt.Errorf("a scan may cover at most %d addresses", MaxScanHosts)
		}
	}
	addrs := make(chan netip.Addr)
	var mu sync.Mutex
	var open []netip.AddrPort
	var wg sync.WaitGroup
	for range scanWorkers {
		wg.Go(func() {
			d := net.Dialer{Timeout: timeout}
			for a := range addrs {
				ap := netip.AddrPortFrom(a, port)
				conn, err := d.DialContext(ctx, "tcp", ap.String())
				if err != nil {
					continue
				}
				_ = conn.Close()
				mu.Lock()
				open = append(open, ap)
				mu.Unlock()
			}
		})
	}
feed:
	for _, p := range prefixes {
		for a := p.Addr(); p.Contains(a); a = a.Next() {
			select {
			case addrs <- a:
			case <-ctx.Done():
				break feed
			}
		}
	}
	close(addrs)
	wg.Wait()
	slices.SortFunc(open, func(x, y netip.AddrPort) int { return x.Compare(y) })
	return open, ctx.Err()
}
