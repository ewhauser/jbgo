## oils_failures_allowed: 0
## compare_shells: bash
#
# Here we list tests where different shells disagree with each other. For example we
# this can cause build failures. So even if we don't directly plan on fixing them (ever)
# it can still be useful to keep track of these cases. This is also what separates these
# cases from the cases in the divergence spec tests (we plan on fixing those).

#### `set` output format - ifupdown-ng
export FOO=bar
set | grep bar | head -n 1
## STDOUT:
FOO=bar
## END

#### nested function declaration - xcb-util-renderutil
f() g() { echo 'hi'; }
## STDOUT:
## status: 2

