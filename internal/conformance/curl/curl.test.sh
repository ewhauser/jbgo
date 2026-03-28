#### basic GET
curl -s "${GBASH_CONFORMANCE_CURL_BASE_URL}/plain"

#### redirect follow
curl -s -L "${GBASH_CONFORMANCE_CURL_BASE_URL}/redirect"

#### auth and request headers
curl -s -u user:pass -A 'agent/1.0' -e https://ref.example -b 'a=1; b=2' "${GBASH_CONFORMANCE_CURL_BASE_URL}/inspect/request"

#### data body
curl -s -d 'hello world' "${GBASH_CONFORMANCE_CURL_BASE_URL}/echo/body"

#### data urlencode
curl -s --data-urlencode 'message=hello world' "${GBASH_CONFORMANCE_CURL_BASE_URL}/echo/body"

#### multipart form
printf '%s' 'upload payload' >/tmp/upload.txt
curl -s -F 'file=@/tmp/upload.txt;type=text/plain' "${GBASH_CONFORMANCE_CURL_BASE_URL}/inspect/form"

#### upload file
printf '%s' 'upload payload' >/tmp/upload.txt
curl -s -T /tmp/upload.txt "${GBASH_CONFORMANCE_CURL_BASE_URL}/echo/body"

#### output file
curl -s -o /tmp/out.txt "${GBASH_CONFORMANCE_CURL_BASE_URL}/files/report.txt"
cat /tmp/out.txt

#### remote name
curl -s -O "${GBASH_CONFORMANCE_CURL_BASE_URL}/files/report.txt"
cat report.txt

#### write out with output file
curl -s -o /tmp/out.txt -w '%{http_code} %{content_type} %{url_effective} %{size_download}' "${GBASH_CONFORMANCE_CURL_BASE_URL}/files/report.txt"
printf '\n'
cat /tmp/out.txt

#### include headers
response="$(curl -s -i "${GBASH_CONFORMANCE_CURL_BASE_URL}/include")"
printf '%s\n' "$response" | sed 's/\r$//' | grep -E '^(HTTP/1.1 200 OK|Content-Type: text/plain|X-Test: include|included-body)$'

#### head request
response="$(curl -s -I "${GBASH_CONFORMANCE_CURL_BASE_URL}/head")"
printf '%s\n' "$response" | sed 's/\r$//' | grep -E '^(HTTP/1.1 200 OK|Content-Type: text/plain|X-Test: head-only)$'

#### fail with silent show-error
set +e
stderr="$(curl -f -sS "${GBASH_CONFORMANCE_CURL_BASE_URL}/status/404" 2>&1)"
status=$?
set -e
printf '%s\n' "$status"
printf '%s\n' "$stderr"
