module github.com/ewhauser/gbash/contrib/jq

go 1.26.1

require (
	github.com/ewhauser/gbash v0.0.18
	github.com/itchyny/gojq v0.12.18
)

require (
	github.com/creack/pty v1.1.24 // indirect
	github.com/go-quicktest/qt v1.101.0 // indirect
	github.com/google/go-cmp v0.5.9 // indirect
	github.com/itchyny/timefmt-go v0.1.7 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/term v0.40.0 // indirect
)

replace github.com/ewhauser/gbash => ../..
