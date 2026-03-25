module github.com/ewhauser/gbash/examples

go 1.26.0

require (
	github.com/ewhauser/gbash v0.0.29
	github.com/ewhauser/gbash/contrib/bashtool v0.0.29
	github.com/ewhauser/gbash/contrib/extras v0.0.29
	github.com/ewhauser/gbash/contrib/sqlite3 v0.0.29
	github.com/klauspost/compress v1.18.4
	github.com/ncruces/go-sqlite3 v0.31.1
	github.com/openai/openai-go v1.12.0
	go.opentelemetry.io/otel v1.40.0
	go.opentelemetry.io/otel/exporters/stdout/stdoutlog v0.16.0
	go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.40.0
	go.opentelemetry.io/otel/log v0.16.0
	go.opentelemetry.io/otel/sdk v1.40.0
	go.opentelemetry.io/otel/sdk/log v0.16.0
	go.opentelemetry.io/otel/trace v1.40.0
	golang.org/x/term v0.40.0
	google.golang.org/adk v0.6.0
	google.golang.org/genai v1.49.0
)

require (
	cloud.google.com/go v0.123.0 // indirect
	cloud.google.com/go/auth v0.17.0 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	github.com/JohannesKaufmann/dom v0.2.0 // indirect
	github.com/JohannesKaufmann/html-to-markdown/v2 v2.5.0 // indirect
	github.com/a8m/envsubst v1.4.3 // indirect
	github.com/agext/levenshtein v1.2.1 // indirect
	github.com/alecthomas/participle/v2 v2.1.4 // indirect
	github.com/apparentlymart/go-textseg/v15 v15.0.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dimchansky/utfbom v1.1.1 // indirect
	github.com/elliotchance/orderedmap v1.8.0 // indirect
	github.com/ewhauser/gbash/contrib/awk v0.0.29 // indirect
	github.com/ewhauser/gbash/contrib/htmltomarkdown v0.0.29 // indirect
	github.com/ewhauser/gbash/contrib/jq v0.0.29 // indirect
	github.com/ewhauser/gbash/contrib/python v0.0.29 // indirect
	github.com/ewhauser/gbash/contrib/yq v0.0.29 // indirect
	github.com/ewhauser/gomonty v0.0.11 // indirect
	github.com/fatih/color v1.18.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/go-ini/ini v1.67.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/goccy/go-json v0.10.5 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/google/safehtml v0.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.6 // indirect
	github.com/googleapis/gax-go/v2 v2.15.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/hashicorp/hcl/v2 v2.24.0 // indirect
	github.com/itchyny/gojq v0.12.18 // indirect
	github.com/itchyny/timefmt-go v0.1.7 // indirect
	github.com/jinzhu/copier v0.4.0 // indirect
	github.com/magiconair/properties v1.8.10 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mikefarah/yq/v4 v4.52.4 // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/ncruces/julianday v1.0.0 // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/tetratelabs/wazero v1.11.0 // indirect
	github.com/tidwall/gjson v1.14.4 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	github.com/zclconf/go-cty v1.17.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.63.0 // indirect
	go.opentelemetry.io/otel/metric v1.40.0 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.3 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/mod v0.33.0 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	golang.org/x/tools v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260128011058-8636f8732409 // indirect
	google.golang.org/grpc v1.78.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/op/go-logging.v1 v1.0.0-20160211212156-b2cb9fa56473 // indirect
	rsc.io/omap v1.2.0 // indirect
	rsc.io/ordered v1.1.1 // indirect
)

replace github.com/ewhauser/gbash => ../

replace github.com/ewhauser/gbash/contrib/bashtool => ../contrib/bashtool

replace github.com/ewhauser/gbash/contrib/extras => ../contrib/extras

replace github.com/ewhauser/gbash/contrib/sqlite3 => ../contrib/sqlite3

replace github.com/ewhauser/gbash/contrib/awk => ../contrib/awk

replace github.com/ewhauser/gbash/contrib/htmltomarkdown => ../contrib/htmltomarkdown

replace github.com/ewhauser/gbash/contrib/jq => ../contrib/jq

replace github.com/ewhauser/gbash/contrib/yq => ../contrib/yq

replace github.com/ewhauser/gbash/contrib/python => ../contrib/python
