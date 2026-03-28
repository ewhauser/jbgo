#### inline program execution
printf 'a b\nc d\n' | awk '{ print $2 }'

#### program files field separator vars and multi file program
printf 'a,b\nc,d\n' > in.csv
printf 'BEGIN { print prefix }\n' > one.awk
printf '{ print $2 }\n' > two.awk
awk -F, -v prefix=rows -f one.awk -f two.awk in.csv

#### multi file nr fnr boundaries
printf 'a\nb\n' > one.txt
printf 'c\nd\n' > two.txt
awk 'NR==FNR { next } { print }' one.txt two.txt

#### filename and fnr reset per input file
printf 'a\nb\n' > one.txt
printf 'c\nd\n' > two.txt
awk '{ print FILENAME ":" FNR ":" $0 }' one.txt two.txt

#### stdin marker with named file
printf 'file\n' > one.txt
printf 'stdin\n' | awk '{ print $0 }' - one.txt

#### input assignment args
printf 'a\nb\n' > names.txt
awk '{ print prefix ":" $0 }' prefix=hi names.txt

#### parse error failure
set +e
stderr="$(awk 'BEGIN {' 2>&1 >/dev/null)"
rc=$?
set -e
printf 'rc=%s\n' "$rc"
case "$stderr" in
  "") printf 'missing-stderr\n' ;;
  *) printf 'stderr\n' ;;
esac

#### missing file failure
set +e
stderr="$(awk '{ print }' missing.txt 2>&1 >/dev/null)"
rc=$?
set -e
printf 'rc=%s\n' "$rc"
case "$stderr" in
  *"missing.txt"*) printf 'missing-file\n' ;;
  *) printf 'missing-filename\n' ;;
esac

#### system execution denied by sandbox
awk 'BEGIN { system("printf sys\n") }'

#### getline from non input file denied by sandbox
printf 'allowed\n' > one.txt
printf 'blocked\n' > extra.txt
awk 'BEGIN { getline line < "extra.txt"; print line }' one.txt

#### output redirection denied by sandbox
rm -f out.txt
awk 'BEGIN { print "hello" > "out.txt" }'
test -f out.txt && cat out.txt
