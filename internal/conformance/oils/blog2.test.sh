## compare_shells: bash

#
# Tests for the blog.
#

#### -a
[ -a ]
echo status=$?
## stdout: status=0

#### -a -a
[ -a -a ]
echo status=$?
## stdout: status=1

#### -a -a -a
[ -a -a -a ]
echo status=$?
## stdout: status=0

#### -a -a -a -a
[ -a -a -a -a ]
echo status=$?
## STDOUT:
status=1
## END

#### -a -a -a -a -a
[ -a -a -a -a -a ]
echo status=$?
## stdout: status=1

#### -a -a -a -a -a -a
[ -a -a -a -a -a -a ]
echo status=$?
## STDOUT:
status=1
## END

#### -a -a -a -a -a -a -a
[ -a -a -a -a -a -a -a ]
echo status=$?
## STDOUT:
status=1
## END

#### -a -a -a -a -a -a -a -a
[ -a -a -a -a -a -a -a -a ]
echo status=$?
## stdout: status=1
