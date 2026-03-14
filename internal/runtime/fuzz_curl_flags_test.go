package runtime

import (
	"context"
	"testing"

	"github.com/ewhauser/gbash/network"
	"github.com/ewhauser/gbash/policy"
)

func FuzzCurlFlagsCommand(f *testing.F) {
	rt := newCurlFuzzRuntime(f)

	seeds := [][]byte{
		[]byte("alpha beta"),
		[]byte("binary\x00payload"),
		[]byte("hello=world&x=1"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawData []byte) {
		session := newFuzzSession(t, rt)
		value := sanitizeFuzzToken(string(rawData))
		payload := clampFuzzData(rawData)
		inputPath := "/tmp/curl-input.bin"
		writeSessionFile(t, session, inputPath, payload)

		script := []byte(
			"curl -s -A " + shellQuote(value) + " -b 'a=1' -c /tmp/cookies.txt -w '\\n%{http_code}' https://api.example.com/raw >/tmp/curl-basic.out\n" +
				"curl -s --data-urlencode " + shellQuote("message="+value) + " --connect-timeout 0.1 https://api.example.com/post >/tmp/curl-post.out\n" +
				"curl -s --data-binary " + shellQuote(value) + " -m 0.1 https://api.example.com/binary >/tmp/curl-binary.out\n" +
				"curl -s -F " + shellQuote("file=@"+inputPath+";type=application/octet-stream") + " https://api.example.com/form >/tmp/curl-form.out\n" +
				"curl -s -T " + shellQuote(inputPath) + " -O https://api.example.com/files/payload.bin >/tmp/curl-upload.out\n" +
				"curl -I https://api.example.com/head >/tmp/curl-head.out\n" +
				"curl -v https://api.example.com/verbose >/tmp/curl-verbose.out\n",
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}

func newCurlFuzzRuntime(tb testing.TB) *Runtime {
	tb.Helper()

	client := &curlStubClient{
		do: func(_ context.Context, req *network.Request) (*network.Response, error) {
			body := append([]byte("reply:"), req.Body...)
			return &network.Response{
				StatusCode: 200,
				Status:     "200 OK",
				Headers: map[string]string{
					"Content-Type": "application/octet-stream",
					"Set-Cookie":   "session=fuzz; Path=/",
				},
				Body: body,
				URL:  req.URL,
			}, nil
		},
	}

	rt, err := New(WithConfig(&Config{
		NetworkClient: client,
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:  []string{"/"},
			WriteRoots: []string{"/"},
			Limits: policy.Limits{
				MaxCommandCount:      200,
				MaxGlobOperations:    2000,
				MaxLoopIterations:    200,
				MaxSubstitutionDepth: 8,
				MaxStdoutBytes:       16 << 10,
				MaxStderrBytes:       16 << 10,
				MaxFileBytes:         128 << 10,
			},
		}),
	}))
	if err != nil {
		tb.Fatalf("New() error = %v", err)
	}
	return rt
}
