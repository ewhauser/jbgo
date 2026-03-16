#!/bin/bash

stdout=${1-STDOUT}
stderr=${2-STDERR}
status=${3-0}

printf '%s\n' "$stdout"
printf '%s\n' "$stderr" >&2
exit "$status"
