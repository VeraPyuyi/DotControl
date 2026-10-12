package experiment

import "strings"

// One AWK process performs this bounded fixture on one CPU. Cooperative stop
// avoids the check-then-kill PID reuse race entirely: no process is signalled.
// Worker and child ignore SSH hangup via nohup; all descriptors are detached.
const workerScript = `#!/bin/sh
set -euC
umask 077
PATH=/usr/bin:/bin
export PATH
mkdir -m 700 worker.claim || exit 80
printf '%s\n' "$$" > pid
awk '{print $22}' /proc/$$/stat > starttime
cat /proc/sys/kernel/random/boot_id > boot
set +e
awk 'BEGIN {
  sum=0
  for(i=1;i<=1000000;i++) {
    sum+=i
    if(i%10000==0) {
      if ((getline stop < "stop.request")>0 && stop=="stop") {close("stop.request");exit 130}
      close("stop.request")
    }
  }
  printf "%.0f\n",sum
}' > result 2> log
code=$?
set -e
if [ "$code" = 0 ]; then
 sha256sum result | awk '{print $1}' > result.sha256
 if [ "$(wc -c < result)" = 13 ] && [ "$(cat result)" = 500000500000 ]; then printf 'true\n' > expected; else printf 'false\n' > expected; fi
fi
printf '%s\n' "$code" > exit
`

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }

func remoteScript(base string, r Request) string {
	prefix := "set -eu\numask 077\nPATH=/usr/bin:/bin\nexport PATH\nLC_ALL=C\nexport LC_ALL\nbase=" + quote(base) + "\nid=" + quote(r.ExperimentID) + "\nbinding=" + quote(r.Binding) + "\nnonce=" + quote(r.Nonce) + "\nop=" + quote(r.Operation) + "\n"
	return prefix + `
emit() {
 printf '{"experiment_id":"%s","state":"%s","binding":"%s","nonce":"%s","expected_check":false}\n' "$id" "$1" "$binding" "$nonce"
 exit 0
}
[ "$(uname -s)" = Linux ] || emit unsafe_remote_state
uid=$(id -u)
# Ancestors may be root-owned or user-owned. Shared writable parents are only
# accepted when sticky, as in /tmp. No symlink component is ever followed.
p=$base
while :; do
 [ -d "$p" ] && [ ! -L "$p" ] || emit unsafe_remote_state
 owner=$(stat -c %u -- "$p")
 [ "$owner" = "$uid" ] || [ "$owner" = 0 ] || emit unsafe_remote_state
 mode=$(stat -c %a -- "$p")
 if [ "$((0$mode & 0022))" -ne 0 ] && [ "$((0$mode & 01000))" -eq 0 ]; then emit unsafe_remote_state; fi
 [ "$p" != / ] || break
 p=$(dirname -- "$p")
done
cd -P -- "$base" || emit unsafe_remote_state
[ "$PWD" = "$base" ] || emit unsafe_remote_state
dir=dotcontrol-$id
new=no
if [ ! -e "$dir" ] && [ ! -L "$dir" ]; then
 if [ "$op" = submit ]; then
  if mkdir -m 700 -- "$dir" 2>/dev/null; then new=yes; fi
 else emit absent
 fi
fi
[ -d "$dir" ] && [ ! -L "$dir" ] || emit unsafe_remote_state
[ "$(stat -c %u -- "$dir")" = "$uid" ] && [ "$(stat -c %a -- "$dir")" = 700 ] || emit unsafe_remote_state
cd -P -- "$dir" || emit unsafe_remote_state
[ "$PWD" = "$base/$dir" ] || emit unsafe_remote_state
regular() {
 [ -f "$1" ] && [ ! -L "$1" ] && [ "$(stat -c %u -- "$1")" = "$uid" ] && [ "$(stat -c %h -- "$1")" = 1 ] && [ "$(stat -c %a -- "$1")" = 600 ]
}
# Reject abnormal pre-existing names before any read or write, including
# incomplete worker files. Private mode prevents other users replacing them.
for f in binding worker.sh pid starttime boot result result.sha256 expected exit log stop.request; do
 if [ -e "$f" ] || [ -L "$f" ]; then regular "$f" || emit unsafe_remote_state; fi
done
if [ -e worker.claim ] || [ -L worker.claim ]; then
 [ -d worker.claim ] && [ ! -L worker.claim ] && [ "$(stat -c %u worker.claim)" = "$uid" ] && [ "$(stat -c %a worker.claim)" = 700 ] || emit unsafe_remote_state
fi
if [ "$new" = yes ]; then
 set -C
 printf '%s\n' "$binding" > binding
 cat > worker.sh <<'DOTCONTROL_FIXED_WORKER'
` + workerScript + `DOTCONTROL_FIXED_WORKER
 nohup sh ./worker.sh </dev/null >/dev/null 2>/dev/null &
 emit claimed
fi
regular binding || emit unsafe_remote_state
[ "$(wc -c < binding)" = 65 ] && [ "$(cat binding)" = "$binding" ] || emit binding_conflict
if [ -f exit ]; then
 [ "$(wc -c < exit)" -le 4 ] || emit unsafe_remote_state
 code=$(cat exit)
 case "$code" in ''|*[!0-9]*) emit unsafe_remote_state;; esac
 [ "$code" -le 255 ] || emit unsafe_remote_state
 if [ "$code" = 0 ]; then
  for f in result result.sha256 expected; do regular "$f" || emit unsafe_remote_state; done
  [ "$(wc -c < result)" = 13 ] && [ "$(cat result)" = 500000500000 ] || emit unsafe_remote_state
  [ "$(wc -c < result.sha256)" = 65 ] && [ "$(wc -c < expected)" = 5 ] && [ "$(cat expected)" = true ] || emit unsafe_remote_state
  hash=$(sha256sum result | awk '{print $1}')
  [ "$hash" = "$(cat result.sha256)" ] || emit unsafe_remote_state
  printf '{"experiment_id":"%s","state":"completed","binding":"%s","nonce":"%s","exit_code":0,"result":"500000500000\\n","result_sha256":"%s","expected_check":true}\n' "$id" "$binding" "$nonce" "$hash"
 else
  state=failed
  [ "$code" != 130 ] || state=stopped
  printf '{"experiment_id":"%s","state":"%s","binding":"%s","nonce":"%s","exit_code":%s,"expected_check":false}\n' "$id" "$state" "$binding" "$nonce" "$code"
 fi
 exit 0
fi
alive=no
if regular pid && regular starttime && regular boot; then
 if [ "$(wc -c < pid)" -le 12 ] && [ "$(wc -c < starttime)" -le 24 ] && [ "$(wc -c < boot)" = 37 ]; then
  pid=$(cat pid)
  start=$(cat starttime)
  case "$pid:$start" in *[!0-9:]*|:*|*:) emit unsafe_remote_state;; esac
  if [ -d "/proc/$pid" ] && [ "$(stat -c %u "/proc/$pid")" = "$uid" ] && [ "$(cat boot)" = "$(cat /proc/sys/kernel/random/boot_id)" ]; then
   current=$(awk '{print $22}' "/proc/$pid/stat" 2>/dev/null || true)
   if [ "$current" = "$start" ] && [ "$(readlink "/proc/$pid/cwd" 2>/dev/null || true)" = "$PWD" ]; then alive=yes; fi
  fi
 fi
fi
if [ "$op" = stop ]; then
 [ "$alive" = yes ] || emit stop_unavailable
 if [ ! -e stop.request ]; then (set -C; printf 'stop\n' > stop.request) || emit stop_unavailable; fi
 regular stop.request && [ "$(wc -c < stop.request)" = 5 ] && [ "$(cat stop.request)" = stop ] || emit unsafe_remote_state
 emit stop_requested
fi
if [ "$alive" = yes ]; then emit running; fi
emit outcome_unknown
`
}
