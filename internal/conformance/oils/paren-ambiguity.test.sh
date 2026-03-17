## compare_shells: bash dash mksh zsh ash
## oils_failures_allowed: 3

#### (( closed with )) after multiple lines is parse error - #2337

$SH -c '
(( echo 1
echo 2
(( x ))
: $(( x ))
echo 3
))
'
if test $? -ne 0; then
  echo ok
fi

## STDOUT:
ok
## END
## OK dash/ash STDOUT:
1
2
3
## END

#### $(( closed with )) after multiple lines is parse error - #2337

$SH -c '
echo $(( echo 1
echo 2
(( x ))
: $(( x ))
echo 3
))
'
if test $? -ne 0; then
  echo ok
fi

## STDOUT:
ok
## END

