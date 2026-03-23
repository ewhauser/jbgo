module github.com/ewhauser/gbash/contrib/nodejs

go 1.26.0

require (
	github.com/dop251/goja v0.0.0-20260311135729-065cd970411c
	github.com/dop251/goja_nodejs v0.0.0-20260212111938-1f56ff5bcf14
	github.com/ewhauser/gbash v0.0.27
)

require (
	github.com/dlclark/regexp2 v1.11.4 // indirect
	github.com/dop251/base64dec v0.0.0-20231022112746-c6c9f9a96217 // indirect
	github.com/go-sourcemap/sourcemap v2.1.4+incompatible // indirect
	github.com/google/pprof v0.0.0-20240727154555-813a5fbdbec8 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/term v0.40.0 // indirect
	golang.org/x/text v0.34.0 // indirect
)

replace github.com/ewhauser/gbash => ../..
