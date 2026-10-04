#!/usr/bin/env bash
# smoke_check.sh VERSION: after an install, prove the installed jevx is that release, the agent skill is in place, and
# a real call goes out and comes back as a verdict (against scripts/smoke_stub.py, so no key is needed).
set -euo pipefail
want=${1:?usage: smoke_check.sh vX.Y.Z}
dir=$(cd "$(dirname "$0")" && pwd)
py=$(command -v python3 || command -v python)
"$py" "$dir/smoke_stub.py" 21999 & stub=$!
trap 'kill $stub 2>/dev/null || true' EXIT
for _ in $(seq 1 50); do (echo >/dev/tcp/127.0.0.1/21999) 2>/dev/null && break; sleep 0.1; done

got=$(jevx version)
[ "$got" = "jevx $want" ] || { echo "FAIL version: got '$got', want 'jevx $want'"; exit 1; }
echo "ok version: $got"

[ -f "$HOME/.claude/skills/jevx/SKILL.md" ] || { echo "FAIL skill: $HOME/.claude/skills/jevx/SKILL.md missing"; exit 1; }
echo "ok skill: ~/.claude/skills/jevx/SKILL.md"

export JEVX_CONFIG="${RUNNER_TEMP:-$(mktemp -d)}/jevx-smoke.json"
echo '{}' > "$JEVX_CONFIG"
jevx profile add stub http://127.0.0.1:21999/v1/systemone --model stub >/dev/null
out=$(echo "Prod is down" | jevx is "Is this urgent?" --profile stub --no-context --fresh) && code=0 || code=$?
[ "$out" = "yes 0.90" ] && [ $code = 0 ] || { echo "FAIL first answer: got '$out' exit $code, want 'yes 0.90' exit 0"; exit 1; }
echo "ok first answer: $out (exit $code)"

table=$(printf 'a\nb\n' | jevx ask --profile stub --no-context --fresh --lines - --noul x="Is this an error?")
[ "$(printf '%s\n' "$table" | grep -c '^yes ')" = 2 ] || { echo "FAIL batch table:"; echo "$table"; exit 1; }
echo "ok batch table: 2 rows"
