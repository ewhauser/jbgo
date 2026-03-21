## compare_shells: bash
## oils_failures_allowed: 0

#### no expansion
echo {foo}
## stdout: {foo}

#### incomplete trailing expansion
echo {a,b}_{
## stdout: a_{ b_{

#### partial leading expansion
echo }_{a,b}
## stdout: }_a }_b

#### partial leading expansion 2
echo {x}_{a,b}
## stdout: {x}_a {x}_b

#### } in expansion
# hm they treat this the SAME.  Leftmost { is matched by first }, and then
# there is another } as the postfix.
echo {a,b}}
## stdout: a} b}
## status: 0

#### single expansion
echo {foo,bar}
## stdout: foo bar

#### double expansion
echo {a,b}_{c,d}
## stdout: a_c a_d b_c b_d

#### triple expansion
echo {0,1}{0,1}{0,1}
## stdout: 000 001 010 011 100 101 110 111

#### double expansion with single and double quotes
echo {'a',b}_{c,"d"}
## stdout: a_c a_d b_c b_d

#### expansion with mixed quotes
echo -{\X"b",'cd'}-
## stdout: -Xb- -cd-

#### expansion with simple var
a=A
echo -{$a,b}-
## stdout: -A- -b-

#### double expansion with simple var -- bash bug
# bash is inconsistent with the above
a=A
echo {$a,b}_{c,d}
## stdout: A_c A_d b_c b_d
## BUG bash stdout: b_c b_d

#### double expansion with braced variable
# This fixes it
a=A
echo {${a},b}_{c,d}
## stdout: A_c A_d b_c b_d

#### double expansion with literal and simple var
a=A
echo {_$a,b}_{c,d}
## stdout: _A_c _A_d b_c b_d
## BUG bash stdout: _ _ b_c b_d

#### expansion with command sub
a=A
echo -{$(echo a),b}-
## stdout: -a- -b-

#### expansion with arith sub
a=A
echo -{$((1 + 2)),b}-
## stdout: -3- -b-

#### double expansion with escaped literals
a=A
echo -{\$,\[,\]}-
## stdout: -$- -[- -]-

#### { in expansion
# first { is a prefix.  I think it's harder to read, and \{{a,b} should be
# required.
echo {{a,b}
## stdout: {{a,b}

#### quoted { in expansion
echo \{{a,b}
## stdout: {a {b

#### Empty expansion
echo a{X,,Y}b
## stdout: aXb ab aYb

#### Empty alternative
# AFTER variable substitution.
argv.sh {X,,Y,}
## stdout: ['X', 'Y']
## status: 0

#### Empty alternative with empty string suffix
# AFTER variable substitution.
argv.sh {X,,Y,}''
## stdout: ['X', '', 'Y', '']
## status: 0

#### nested brace expansion
echo -{A,={a,b}=,B}-
## stdout: -A- -=a=- -=b=- -B-

#### triple nested brace expansion
echo -{A,={a,.{x,y}.,b}=,B}-
## stdout: -A- -=a=- -=.x.=- -=.y.=- -=b=- -B-

#### nested and double brace expansion
echo -{A,={a,b}{c,d}=,B}-
## stdout: -A- -=ac=- -=ad=- -=bc=- -=bd=- -B-

#### expansion on RHS of assignment
# I think bash's behavior is more consistent.  No splitting either.
v={X,Y}
echo $v
## stdout: {X,Y}

#### no expansion with RHS assignment
{v,x}=X
## status: 127
## stdout-json: ""

#### Tilde expansion
HOME=/home/foo
echo ~
HOME=/home/bar
echo ~
## STDOUT:
/home/foo
/home/bar
## END

#### Tilde expansion with brace expansion

# The brace expansion happens FIRST.  After that, the second token has tilde
# FIRST, so it gets expanded.  The first token has an unexpanded tilde, because
# it's not in the leading position.

HOME=/home/bob

# Command

echo {foo~,~}/bar

# Loop

for x in {foo~,~}/bar; do
  echo -- $x
done

# Array

a=({foo~,~}/bar)

for y in "${a[@]}"; do
  echo "== $y"
done

## STDOUT:
foo~/bar /home/bob/bar
-- foo~/bar
-- /home/bob/bar
== foo~/bar
== /home/bob/bar
## END

#### Two kinds of tilde expansion

HOME=/home/bob

# Command
echo ~{/src,root}

# Loop

for x in ~{/src,root}; do
  echo -- $x
done

# Array

a=(~{/src,root})

for y in "${a[@]}"; do
  echo "== $y"
done

## STDOUT:
/home/bob/src /root
-- /home/bob/src
-- /root
== /home/bob/src
== /root
## END

#### Tilde expansion come before var expansion
HOME=/home/bob
foo=~
echo $foo
foo='~'
echo $foo
# In the second instance, we expand into a literal ~, and since var expansion
# comes after tilde expansion, it is NOT tried again.
## STDOUT:
/home/bob
~
## END

#### Number range expansion
echo -{1..8..3}-
echo -{1..10..3}-
## STDOUT:
-1- -4- -7-
-1- -4- -7- -10-

#### Ascending number range expansion with negative step is invalid
echo -{1..8..-3}-
## stdout: -1- -4- -7-

#### regression: -1 step disallowed
echo -{1..4..-1}-
## stdout: -1- -2- -3- -4-

#### regression: 0 step disallowed
echo -{1..4..0}-
## stdout-json: ""
## status: 2
## BUG bash stdout: -1- -2- -3- -4-

#### Descending number range expansion with positive step is invalid
echo -{8..1..3}-
## stdout: -8- -5- -2-

#### Descending number range expansion with negative step
echo -{8..1..-3}-
## stdout: -8- -5- -2-

#### Singleton ranges
echo {1..1}-
echo {-9..-9}-
echo {-9..-9..3}-
echo {-9..-9..-3}-
echo {a..a}-
## STDOUT:
1-
-9-
-9-
-9-
a-
## END

#### Singleton char ranges with steps
echo {a..a..2}-
echo {a..a..-2}-
## STDOUT:
a-
a-
## END

#### Char range expansion
echo -{a..e}-
## stdout: -a- -b- -c- -d- -e-

#### Char range expansion with step
echo -{a..e..2}-
## stdout: -a- -c- -e-

#### Char ranges with steps of the wrong sign
echo -{a..e..-2}-
echo -{e..a..2}-
## STDOUT:
-a- -c- -e-
-e- -c- -a-
## END

#### Mixed case char expansion is invalid
echo -{z..A}-
echo -{z..A..2}-
## stdout-json: ""
## status: 1
# Bash 5.3 also prints bad substitution diagnostics on both lines.

#### Descending char range expansion
echo -{e..a..-2}-
## stdout: -e- -c- -a-

#### Fixed width number range expansion
echo -{01..03}-
echo -{09..12}-  # doesn't become -012-, fixed width
echo -{12..07}-
## STDOUT:
-01- -02- -03-
-09- -10- -11- -12-
-12- -11- -10- -09- -08- -07-
## END

#### Inconsistent fixed width number range expansion
echo -{01..003}-
## stdout: -001- -002- -003-

#### Inconsistent fixed width number range expansion
echo -{01..3}-
## stdout: -01- -02- -03-

#### Adjacent comma and range works
echo -{a,b}{1..3}-
## STDOUT:
-a1- -a2- -a3- -b1- -b2- -b3-
## END

#### Range inside comma works
echo -{a,_{1..3}_,b}-
## STDOUT:
-a- -_1_- -_2_- -_3_- -b-
## END

#### Mixed comma and range doesn't work
echo -{a,b,1..3}-
## STDOUT:
-a- -b- -1..3-
## END

#### comma and invalid range (adjacent and nested)
echo -{a,b}{1...3}-
echo -{a,{1...3}}-
echo {a,b}{}
## STDOUT:
-a{1...3}- -b{1...3}-
-a- -{1...3}-
a{} b{}
## END
# case below.

echo -{a,b}\{1...3\}-
echo -{a,\{1...3\}}-
echo {a,b}\{\}
## STDOUT:
-a{1...3}- -b{1...3}-
-a- -{1...3}-
a{} b{}
## END

#### Side effect in expansion
# bash is the only one that does it first.  I guess since this is
# non-POSIX anyway, follow bash?
i=0
echo {a,b,c}-$((i++))
## stdout: a-0 b-1 c-2

#### Invalid brace expansions don't expand
echo {1.3}
echo {1...3}
echo {1__3}
## STDOUT:
{1.3}
{1...3}
{1__3}
## END

#### Invalid brace expansions mixing characters and numbers
echo {1..a}
echo {z..3}
## STDOUT:
{1..a}
{z..3}
## END
