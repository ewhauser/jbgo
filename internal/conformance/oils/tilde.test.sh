## compare_shells: bash

#### ~ expansion in assignment
HOME=/home/bob
a=~/src
echo $a
## stdout: /home/bob/src

#### ~ expansion in readonly assignment
# http://stackoverflow.com/questions/8441473/tilde-expansion-doesnt-work-when-i-logged-into-gui
HOME=/home/bob
readonly const=~/src
echo $const
## stdout: /home/bob/src

#### No ~ expansion in dynamic assignment
HOME=/home/bob
binding='const=~/src'
readonly "$binding"
echo $const
## stdout: ~/src

#### No tilde expansion in word that looks like assignment but isn't
# bash fixes this in POSIX mode (gah).
# http://lists.gnu.org/archive/html/bug-bash/2016-06/msg00001.html
HOME=/home/bob
echo x=~
## stdout: x=~

#### tilde expansion of word after redirect
HOME=$TMP
echo hi > ~/tilde1.txt
cat $HOME/tilde1.txt | wc -c
## stdout: 3
## status: 0

#### other user
echo ~nonexistent
## stdout: ~nonexistent

#### ${undef:-~}
HOME=/home/bar
echo ${undef:-~}
echo ${HOME:+~/z}
echo "${undef:-~}"
echo ${undef:-"~"}
## STDOUT:
/home/bar
/home/bar/z
~
~
## END

#### ${x//~/~root}
HOME=/home/bar
x=~
echo ${x//~/~root}

# gah there is some expansion, what the hell
echo ${HOME//~/~root}

x=[$HOME]
echo ${x//~/~root}

## STDOUT:
/root
/root
[/root]
## END

#### x=foo:~ has tilde expansion
HOME=/home/bar
x=foo:~
echo $x
echo "$x"  # quotes don't matter, the expansion happens on assignment?
x='foo:~'
echo $x

x=foo:~,  # comma ruins it, must be /
echo $x

x=~:foo
echo $x

# no tilde expansion here
echo foo:~
## STDOUT:
foo:/home/bar
foo:/home/bar
foo:~
foo:~,
/home/bar:foo
foo:~
## END

#### a[x]=foo:~ has tilde expansion

HOME=/home/bar
declare -a a
a[0]=foo:~
echo ${a[0]}

declare -A A
A['x']=foo:~
echo ${A['x']}

## STDOUT:
foo:/home/bar
foo:/home/bar
## END

#### tilde expansion an assignment keyword
HOME=/home/bar
f() {
  local x=foo:~
  echo $x
}
f
## STDOUT:
foo:/home/bar
## END

#### x=${undef-~:~}
HOME=/home/bar

x=~:${undef-~:~}
echo $x

## STDOUT:
/home/bar:/home/bar:/home/bar
## END

#### strict tilde
echo ~nonexistent

echo ~nonexistent

echo status=$?
## status: 1
## STDOUT:
~nonexistent
## END

#### temp assignment x=~ env

HOME=/home/bar

xx=~ env | grep xx=

# Does it respect the colon rule too?
xx=~root:~:~ env | grep xx=

## STDOUT:
xx=/home/bar
xx=/root:/home/bar:/home/bar
## END
