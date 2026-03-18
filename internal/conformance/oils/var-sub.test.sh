## compare_shells: bash

# Corner cases in var sub.  Maybe rename this file.

# FILES!

#### Bad var sub
echo ${a&}
## stdout-json: ""
## status: 2

#### Braced block inside ${}
# NOTE: This bug was in bash 4.3 but fixed in bash 4.4.
echo ${foo:-$({ ls /bin/ls; })}
## stdout: /bin/ls

#### Nested ${} 
bar=ZZ
echo ${foo:-${bar}}
## stdout: ZZ

#### Filename redirect with "$@" 
# bash - ambiguous redirect -- yeah I want this error
#   - But I want it at PARSE time?  So is there a special DollarAtPart?
#     MultipleArgsPart?
fun() {
  echo hi > "$@"
}
fun _tmp/var-sub1 _tmp/var-sub2
## status: 1

#### Descriptor redirect to bad "$@"
# All of them give errors:
# bash - ambiguous redirect
set -- '2 3' 'c d'
echo hi 1>& "$@"
## status: 1

#### Here doc with bad "$@" delimiter
# bash - syntax error
#
# What I want is syntax error: bad delimiter!
#
# This means that "$@" should be part of the parse tree then?  Anything that
# involves more than one token.
fun() {
  cat << "$@"
hi
1 2
}
fun 1 2
## status: 2
## stdout-json: ""
