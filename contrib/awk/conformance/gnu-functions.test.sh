#### gensub global replacement
awk 'BEGIN { print gensub("([0-9]+)", "<\\1>", "g", "item42 batch7") }'

#### strftime and mktime
TZ=UTC awk 'BEGIN { print strftime("%Y-%m-%d %H:%M:%S", 0, 1); print mktime("1970 01 02 00 00 00") }'
