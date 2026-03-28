#### system execution in sandbox
awk 'BEGIN { rc = system("echo system-ok"); print "rc=" rc }'

#### getline from arbitrary sandbox file
printf 'extra\n' > extra.txt
awk 'BEGIN { getline line < "extra.txt"; close("extra.txt"); print line }'

#### output redirection writes file
rm -f out.txt
awk 'BEGIN { print "hello" > "out.txt"; close("out.txt") }'
cat out.txt

#### pipe getline
awk 'BEGIN { "echo pipe-read" | getline line; close("echo pipe-read"); print line }'

#### pipe output
printf 'left\n' > in.txt
awk '{ print $0 | "cat > piped.txt"; close("cat > piped.txt") }' in.txt
cat piped.txt
