## compare_shells: dash bash mksh zsh

# Tests for the blog.
#
# Fun game: try to come up with an expression that behaves differently on ALL
# FOUR shells.

#### ${##}
set -- $(seq 25)
echo ${##}
## stdout: 2

#### ${1####}
set -- '####'
echo ${1####}
## stdout: ##

#### ${1#'###'}
set -- '####'
echo ${1#'###'}
## stdout: #

#### Julia example from spec/oil-user-feedback

case $SH in dash|mksh|zsh) exit ;; esac

git-branch-merged() {
  cat <<EOF
  foo
* bar
  baz
  master
EOF
}

shopt -s lastpipe  # required for bash, not OSH

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
## N-I dash/mksh/zsh STDOUT:
## END
