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
	"os"
	"strconv"
)

// setupURLs returns the addresses someone on another machine can open to
// reach first-time setup. The API listens on every interface, so a server
// installed on a network must not advertise localhost: list its own
// non-loopback addresses (IPv4 first), then fall back to the hostname, and
// only then to localhost.
func setupURLs(port int, addrs []net.Addr, hostname string) []string {
	var v4, v6 []string
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipn.IP
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
			continue
		}
		u := "http://" + net.JoinHostPort(ip.String(), strconv.Itoa(port)) + "/setup"
		if ip.To4() != nil {
			v4 = append(v4, u)
		} else {
			v6 = append(v6, u)
		}
	}
	if urls := append(v4, v6...); len(urls) > 0 {
		return urls
	}
	host := hostname
	if host == "" {
		host = "localhost"
	}
	return []string{"http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/setup"}
}

// localSetupURLs reads this machine's interface addresses and hostname.
func localSetupURLs(port int) []string {
	addrs, _ := net.InterfaceAddrs()
	hostname, _ := os.Hostname()
	return setupURLs(port, addrs, hostname)
}
