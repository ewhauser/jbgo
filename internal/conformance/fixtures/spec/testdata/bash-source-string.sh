#!/usr/bin/env bash

argv.sh $BASH_SOURCE  # SimpleVarSub
argv.sh ${BASH_SOURCE}
argv.sh $BASH_LINENO # SimpleVarSub
argv.sh ${BASH_LINENO}

echo ____

# Test with 2 entries
source spec/testdata/bash-source-string2.sh
