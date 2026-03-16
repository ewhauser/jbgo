module github.com/ewhauser/gbash/contrib/awk

go 1.26.1

require (
	github.com/benhoyt/goawk v1.31.0
	github.com/ewhauser/gbash v0.0.18
)

require (
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/term v0.40.0 // indirect
	mvdan.cc/sh/v3 v3.13.0 // indirect
)

replace github.com/ewhauser/gbash => ../..
