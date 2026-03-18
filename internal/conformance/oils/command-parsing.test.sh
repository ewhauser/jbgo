## compare_shells: bash
## legacy_tmp_dir: yes

# Some nonsensical combinations which can all be detected at PARSE TIME.
# TODO: Run the parser on your whole corpus, and then if there are no errors,

#### Prefix env on assignment
f() {
  # NOTE: local treated like a special builtin!
  E=env local v=var
  echo $E $v
}
f
## status: 0
## stdout: env var
## OK bash stdout: var

#### Redirect on assignment (enabled 7/2019)
f() {
  # NOTE: local treated like a special builtin!
  local E=env > _tmp/r.txt
}
rm -f _tmp/r.txt
f
test -f _tmp/r.txt && echo REDIRECTED
## status: 0
## stdout: REDIRECTED

#### Prefix env on control flow
for x in a b c; do
  echo $x
  E=env break
done
## status: 0
## stdout: a

rm -f _tmp/r.txt
for x in a b c; do
  break > _tmp/r.txt
done
if test -f _tmp/r.txt; then
  echo REDIRECTED
else
  echo NO
fi
## status: 0
## stdout: REDIRECTED

rm -f _tmp/r.txt
for x in a b c; do
  break > _tmp/r.txt
done
test -f _tmp/r.txt && echo REDIRECTED
## status: 0
## stdout: REDIRECTED
