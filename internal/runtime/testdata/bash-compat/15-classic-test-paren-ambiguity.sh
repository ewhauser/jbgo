test 0 -eq 0 -a '(' -f ')'
printf 'test-f:%s\n' "$?"

test 0 -eq 0 -a '(' -t ')'
printf 'test-t:%s\n' "$?"

test 0 -eq 0 -a '(' ! ')'
printf 'test-bang:%s\n' "$?"

[ 0 -eq 0 -a '(' -f ')' ]
printf 'bracket-f:%s\n' "$?"

[ 0 -eq 0 -a '(' -t ')' ]
printf 'bracket-t:%s\n' "$?"

[ 0 -eq 0 -a '(' ! ')' ]
printf 'bracket-bang:%s\n' "$?"

test \( x = \( \)
printf 'test-lparen:%s\n' "$?"

[ \( x = \( \) ]
printf 'bracket-lparen:%s\n' "$?"

test \( x -a \( y \) \)
printf 'test-nested-and:%s\n' "$?"

[ \( x -a \( y \) \) ]
printf 'bracket-nested-and:%s\n' "$?"

test \( x -o \( y \) \)
printf 'test-nested-or:%s\n' "$?"

[ \( x -o \( y \) \) ]
printf 'bracket-nested-or:%s\n' "$?"

test \( x -a \( y -a \( z \) \) \)
printf 'test-deep-nested-and:%s\n' "$?"

[ \( x -a \( y -a \( z \) \) \) ]
printf 'bracket-deep-nested-and:%s\n' "$?"

test \( \( x \) \)
printf 'test-nested-word:%s\n' "$?"

[ \( \( x \) \) ]
printf 'bracket-nested-word:%s\n' "$?"
