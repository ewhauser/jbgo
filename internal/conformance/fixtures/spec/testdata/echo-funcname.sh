# For spec/introspect.test.sh
argv.sh '  @' "${FUNCNAME[@]}"
argv.sh '  0' "${FUNCNAME[0]}"
argv.sh '${}' "${FUNCNAME}"
argv.sh '  $' "$FUNCNAME"
