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

#### csv mode
printf 'a,b\nc,d\n' | awk -k '{ print $2 }'
