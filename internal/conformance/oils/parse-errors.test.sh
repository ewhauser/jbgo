## oils_failures_allowed: 3
## compare_shells: bash dash mksh

#### Long Token - 65535 bytes

python2 -c 'print("echo -n %s" % ("x" * 65535))' > tmp.sh
$SH tmp.sh > out
wc --bytes out

## STDOUT:
65535 out
## END

#### Token that's too long for Oils - 65536 bytes

python2 -c 'print("echo -n %s" % ("x" * 65536))' > tmp.sh
$SH tmp.sh > out
echo status=$?
wc --bytes out

## STDOUT:
status=0
65536 out
## END

#### $% is not a parse error
echo $%
## stdout: $%

#### Invalid for loop variable name
for i.j in a b c; do
  echo hi
done
echo done
## stdout-json: ""
## status: 2
## OK mksh status: 1
## BUG bash status: 0
## BUG bash stdout: done

#### bad var name globally isn't parsed like an assignment
# bash and dash disagree on exit code.
FOO-BAR=foo
## status: 127

#### interactive parse error (regression)
flags=''
case $SH in
  bash*|*osh)
    flags='--rcfile /dev/null'
    ;;
esac  
$SH $flags -i -c 'var=)'

## status: 2
## OK mksh status: 1

#### echo =word is allowed
echo =word
## STDOUT:
=word
## END
