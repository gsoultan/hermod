// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package uplink

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// TransportCredentials returns TLS for address, trusting the system's roots
// plus caFile when given. Plaintext is allowed only to a loopback address,
// where nothing on the network can read the token or the data.
func TransportCredentials(address, caFile string, plaintext bool) (credentials.TransportCredentials, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("server address %q: %w", address, err)
	}
	if plaintext {
		if !isLoopback(host) {
			return nil, fmt.Errorf("refusing plaintext to %s: only localhost may be reached without TLS", host)
		}
		return insecure.NewCredentials(), nil
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
	if caFile != "" {
		pem, err := os.ReadFile(caFile) //nolint:gosec // G304: the operator's own -ca-file
		if err != nil {
			return nil, err
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("no certificates found in " + caFile)
		}
		cfg.RootCAs = pool
	}
	return credentials.NewTLS(cfg), nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
