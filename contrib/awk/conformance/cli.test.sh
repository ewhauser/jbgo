#### inline program execution
printf 'a b\nc d\n' | awk '{ print $2 }'

#### program files field separator vars and multi file program
printf 'a,b\nc,d\n' > in.csv
printf 'BEGIN { print prefix }\n' > one.awk
printf '{ print $2 }\n' > two.awk
awk -F, -v prefix=rows -f one.awk -f two.awk in.csv

#### long options for assign and field separator
printf 'a,b\nc,d\n' > in.csv
awk --field-separator=, --assign=prefix=rows 'BEGIN { print prefix } { print $2 }' in.csv

#### ordered multi source loading
printf 'BEGIN { print "file" }\n' > file.awk
printf 'BEGIN { print "include" }\n' > include.awk
awk --source='BEGIN { print "source" }' --file=file.awk --include=include.awk

#### exec file disables arg vars
printf 'BEGIN { print ARGV[1], ARGV[2] }\n' > exec.awk
printf 'x\n' > input.txt
awk -E exec.awk name=value input.txt

#### version info
awk -W version | sed -n '1p'

#### help info
awk --help | sed -n '1,4p'

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
