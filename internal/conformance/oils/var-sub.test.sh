## compare_shells: dash bash mksh

# Corner cases in var sub.  Maybe rename this file.

# NOTE: ZSH has interesting behavior, like echo hi > "$@" can write to TWO
# FILES!

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
# mksh - tries to create '_tmp/var-sub1 _tmp/var-sub2'
# dash - tries to create '_tmp/var-sub1 _tmp/var-sub2'
fun() {
  echo hi > "$@"
}
fun _tmp/var-sub1 _tmp/var-sub2
## status: 1
## OK dash status: 2

#### Descriptor redirect to bad "$@"
# All of them give errors:
# dash - bad fd number, parse error?
# bash - ambiguous redirect
# mksh - illegal file descriptor name
set -- '2 3' 'c d'
echo hi 1>& "$@"
## status: 1
## OK dash status: 2

