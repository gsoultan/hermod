// staticcheck v0.8.1 is the latest release, and it requires an x/tools that
// cannot read the export data Go 1.27.2 writes (pkgbits V5): every package
// fails with "export data version 5 is greater than maximum supported
// version 4". `go install staticcheck@v0.8.1` cannot override a dependency,
// so this module builds the same release against x/tools v0.50.0, the first
// that reads V5. Drop it for a plain `go install` once a staticcheck release
// requires x/tools >= v0.50.0.
module github.com/gsoultan/hermod/tools/staticcheck

go 1.27.2

require honnef.co/go/tools v0.8.1

require (
	github.com/BurntSushi/toml v1.4.1-0.20240526193622-a339e1f7089c // indirect
	golang.org/x/exp/typeparams v0.0.0-20231108232855-2478ac86f678 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/tools v0.50.0 // indirect
)
