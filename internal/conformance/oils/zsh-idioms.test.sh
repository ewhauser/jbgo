## compare_shells: bash zsh mksh

#### zsh var sub is rejected at runtime

eval 'echo z ${(m)foo} z'
echo status=$?

eval 'echo ${x:-${(m)foo}}'
echo status=$?

# double quoted
eval 'echo "${(m)foo}"'
echo status=$?

## STDOUT:
status=1
status=1
status=1
## END

## OK zsh status: 0
## OK zsh STDOUT:
z z
status=0

status=0

status=0
## END

## BUG mksh status: 1
## BUG mksh STDOUT:
## END
