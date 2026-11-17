// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package main

import (
	"net"
	"reflect"
	"testing"
)

func ipNet(s string) net.Addr {
	ip, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	n.IP = ip
	return n
}

func TestSetupURLs(t *testing.T) {
	tests := []struct {
		name     string
		addrs    []net.Addr
		hostname string
		want     []string
	}{
		{
			name:     "network server lists its own addresses, not localhost",
			addrs:    []net.Addr{ipNet("127.0.0.1/8"), ipNet("10.0.2.84/24"), ipNet("::1/128")},
			hostname: "ip-10-0-2-84",
			want:     []string{"http://10.0.2.84:4000/setup"},
		},
		{
			name:  "IPv4 first, global IPv6 bracketed, link-local skipped",
			addrs: []net.Addr{ipNet("fe80::1/64"), ipNet("2001:db8::5/64"), ipNet("169.254.1.1/16"), ipNet("192.168.1.10/24")},
			want:  []string{"http://192.168.1.10:4000/setup", "http://[2001:db8::5]:4000/setup"},
		},
		{
			name:     "no network address falls back to the hostname",
			addrs:    []net.Addr{ipNet("127.0.0.1/8")},
			hostname: "hermod-box",
			want:     []string{"http://hermod-box:4000/setup"},
		},
		{
			name: "nothing at all falls back to localhost",
			want: []string{"http://localhost:4000/setup"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := setupURLs(4000, tt.addrs, tt.hostname); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("setupURLs() = %v, want %v", got, tt.want)
			}
		})
	}
}
