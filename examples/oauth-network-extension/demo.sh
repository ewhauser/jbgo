set -eu

# Scenario 1: no Authorization header comes from the sandbox.
curl -fsS \
  -H 'X-Request-ID: sandbox-demo-42' \
  https://crm.example.test/v1/profile
printf '\n'

# Scenario 2: the sandbox tries to forge its own bearer token.
# The host-side NetworkClient overwrites it before forwarding the request.
curl -fsS \
  -H 'Authorization: Bearer sandbox-forged-token' \
  -H 'X-Request-ID: sandbox-spoof-43' \
  https://crm.example.test/v1/profile
printf '\n'
