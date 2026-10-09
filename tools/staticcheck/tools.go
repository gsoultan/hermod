//go:build tools

// Package staticcheck pins the staticcheck build CI uses. See go.mod for why
// this is its own module rather than `go install ...@version`.
package staticcheck

import _ "honnef.co/go/tools/cmd/staticcheck"
