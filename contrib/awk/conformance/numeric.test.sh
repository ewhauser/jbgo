#### strtonum base detection
awk 'BEGIN { print strtonum("0x10"), strtonum("010"), strtonum("1.5") }'

#### bitwise builtins
awk 'BEGIN { print and(6, 3), or(6, 3), xor(6, 3), compl(0), lshift(3, 2), rshift(8, 2) }'
