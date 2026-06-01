package util

import (
	"context"
	"net"
	"net/mail"
	"net/textproto"
	"strings"
	"time"
)

// VerifyEmailExists performs a best-effort SMTP-level verification by connecting
// to the recipient domain's MX and issuing RCPT TO. Many servers may greylist or
// disable VRFY/RCPT probes; this returns (false, reason) on any definitive
// negative (e.g., 550) and on transport errors provides a reason string.
// It honors the provided context for timeout/cancel.
//
// Why not use gsmail (github.com/gsoultan/gsmail)?
// - gsmail is for *sending* full MIME emails via *authenticated* SMTP (e.g., Gmail).
// - This is a *probe* (anonymous MX:25 RCPT check, no auth/body/delivery/spam risk).
// - Custom: stdlib-only, lightweight (~50 lines), KISS/no bloat.
func VerifyEmailExists(ctx context.Context, address string) (bool, string) {
	// Basic format validation
	parsed, err := mail.ParseAddress(address)
	if err != nil {
		return false, "invalid address format"
	}
	addr := parsed.Address
	at := strings.LastIndex(addr, "@")
	if at < 0 || at == len(addr)-1 {
		return false, "invalid domain"
	}
	domain := addr[at+1:]
	mxRecords, err := net.LookupMX(domain)
	if err != nil || len(mxRecords) == 0 {
		return false, "no MX records"
	}

	dialer := &net.Dialer{}
	for _, mx := range mxRecords {
		select {
		case <-ctx.Done():
			return false, ctx.Err().Error()
		default:
		}
		host := strings.TrimSuffix(mx.Host, ".") + ":25"
		conn, err := dialer.DialContext(ctx, "tcp", host)
		if err != nil {
			// try next MX
			continue
		}
		// Ensure we close the connection
		func() {
			defer conn.Close()
			// Apply a short deadline for the whole interaction
			_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
			tp := textproto.NewConn(conn)
			defer tp.Close()
			// Read greeting
			code, _, err := tp.ReadResponse(220)
			if err != nil || code != 220 {
				return
			}
			// HELO
			if err := tp.PrintfLine("HELO hermod.local"); err != nil {
				return
			}
			if _, _, err := tp.ReadResponse(250); err != nil {
				return
			}
			// MAIL FROM
			if err := tp.PrintfLine("MAIL FROM:<check@hermod.local>"); err != nil {
				return
			}
			if _, _, err := tp.ReadResponse(250); err != nil {
				return
			}
			// RCPT TO
			if err := tp.PrintfLine("RCPT TO:<%s>", addr); err != nil {
				return
			}
			code, msg, err := tp.ReadResponse(250)
			if err == nil && (code == 250 || code == 251 || code == 252) {
				// Accepted
				_ = tp.PrintfLine("QUIT")
				return
			}
			if err != nil {
				// If it is a 550 response, it's likely non-existent
				if te, ok := err.(*textproto.Error); ok {
					if te.Code == 550 {
						return
					}
				}
				// unknown error; try next MX
				return
			}
			// Other codes treat as not sure; leave for next MX
			_ = tp.PrintfLine("QUIT")
			_ = msg // ignore
		}()
		// If we reached here without definitive success, try next MX
	}
	// If none of the MXs accepted, assume not deliverable or undetermined
	return false, "no accepting MX or verification refused"
}
