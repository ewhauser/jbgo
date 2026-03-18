## compare_shells: bash

# Tests for the blog.
#
# Fun game: try to come up with an expression that behaves differently on ALL
# FOUR shells.

#### ${##}
set -- $(seq 25)
echo ${##}
## stdout: 2

#### ${###}
set -- $(seq 25)
echo ${###}
## stdout: 25

#### ${####}
set -- $(seq 25)
echo ${####}
## stdout: 25

#### ${##2}
set -- $(seq 25)
echo ${##2}
## stdout: 5

#### ${###2}
set -- $(seq 25)
echo ${###2}
## stdout: 5

#### ${1####}
set -- '####'
echo ${1####}
## stdout: ##

#### ${1#'###'}
set -- '####'
echo ${1#'###'}
## stdout: #

#### ${#1#'###'}
set -- '####'
echo ${#1#'###'}
## status: 2
## stdout-json: ""

#### Julia example from spec/oil-user-feedback

git-branch-merged() {
  cat <<EOF
  foo
* bar
  baz
  master
EOF
}

shopt -s lastpipe

branches=()  # dangerous when set -e is on
git-branch-merged | while read -r line; do
  line=${line# *}  # strip leading spaces
  if [[ $line != 'master' && ! ${line:0:1} == '*' ]]; then
    branches+=("$line")
  fi
done

if [[ ${#branches[@]} -eq 0 ]]; then
  echo "No merged branches"
else
  echo git branch -D "${branches[@]}"
fi

## STDOUT:
git branch -D foo baz
## END
