#!/usr/bin/env bash
# release.sh: one command from a green main to a verified release.
#   scripts/release.sh patch|minor|major|vX.Y.Z      (task release -- patch)
# Steps: checks (clean main, in sync, vet, tests, site build, optional secret check) -> tag -> push main + tag ->
# wait for the release and Pages workflows -> verify the release assets, npm, and the site header.
# Every version (binaries, npm, the site header) comes from the tag, so nothing is edited by hand.
#
# Environment (all optional):
#   REMOTE               where to push (default git@github.com:muthuishere/jevx.git)
#   SITE_URL             the docs site to verify (default https://muthuishere.github.io/jevx)
#   SEAL_ALLOW           comma list of secret names `sec seal --check` may report as false positives
#   INSTALLER_MIRRORS    space-separated site URLs that serve copies of install.sh / install.cmd; each copy must be
#                        byte-identical to the one on SITE_URL after the release
#   DRY_RUN=1            run the checks and print the next tag, change nothing
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
REMOTE=${REMOTE:-git@github.com:muthuishere/jevx.git}
SITE_URL=${SITE_URL:-https://muthuishere.github.io/jevx}
REPO=muthuishere/jevx
say() { printf '\033[1m== %s\033[0m\n' "$*"; }
die() { printf 'release: %s\n' "$*" >&2; exit 1; }

bump=${1:-}; [ -n "$bump" ] || die "usage: scripts/release.sh patch|minor|major|vX.Y.Z"
last=$(git describe --tags --abbrev=0 --match 'v*')
IFS=. read -r ma mi pa <<<"${last#v}"
case $bump in
  patch) next=v$ma.$mi.$((pa+1)) ;;
  minor) next=v$ma.$((mi+1)).0 ;;
  major) next=v$((ma+1)).0.0 ;;
  v[0-9]*.[0-9]*.[0-9]*) next=$bump ;;
  *) die "bump must be patch, minor, major or vX.Y.Z (got $bump)" ;;
esac
git rev-parse -q --verify "refs/tags/$next" >/dev/null && die "$next already exists"

say "checks for $last -> $next"
[ "$(git branch --show-current)" = main ] || die "not on main"
[ -z "$(git status --porcelain)" ] || die "working tree not clean"
git fetch -q "$REMOTE" main
[ "$(git rev-parse HEAD)" = "$(git rev-parse FETCH_HEAD)" ] || git merge-base --is-ancestor FETCH_HEAD HEAD || die "main is behind the remote: pull first"
go vet ./...
go test ./...
(cd site && { [ -d node_modules ] || npm ci --silent; } && npm run build --silent >/dev/null)
if command -v sec >/dev/null; then
  bad=0
  for f in $(git diff --name-only "$last" HEAD); do
    [ -f "$f" ] || continue
    out=$(sec seal "$f" --check 2>&1) && continue
    names=$(sed -n 's/.*live secret(s): //p' <<<"$out" | tr -d ' ')
    for n in ${names//,/ }; do
      [[ ",${SEAL_ALLOW:-}," == *",$n,"* ]] || { echo "secret check: $f holds $n"; bad=1; }
    done
  done
  [ $bad = 0 ] || die "secret check failed (false positive? add the name to SEAL_ALLOW)"
fi
[ -n "${DRY_RUN:-}" ] && { say "dry run: would tag $next on $(git rev-parse --short HEAD)"; exit 0; }

say "changelog entry and release commit for $next"
# The tag must sit on a commit that GitHub Pages has never deployed: Pages keys a deployment on the commit, so a tag on
# an already-deployed commit "succeeds" but keeps serving the old build (and the old header version). A release commit
# that adds the changelog entry is always new.
log=site/src/content/docs/reference/changelog.md
if grep -q "^## $next " "$log"; then
  # a hand-written entry for this version is already there: keep it; the empty commit still gives the tag a new commit
  git commit -q --allow-empty -m "Release $next"
else
entry=$(mktemp)
{ printf '## %s (%s)\n\n' "$next" "$(date +%Y-%m-%d)"
  git log --no-merges --format='- %s' "$last"..HEAD | grep -v -e '^- Release v' -e '^- sync(box)' || true
  printf '\n'; } > "$entry"
awk -v f="$entry" 'BEGIN{done=0} /^## /&&!done{while((getline l<f)>0)print l; done=1} {print} END{if(!done)while((getline l<f)>0)print l}' "$log" > "$log.new"
mv "$log.new" "$log"; rm -f "$entry"
${EDITOR_RELEASE:-true} "$log" # set EDITOR_RELEASE=vi to edit the generated entry before it is committed
git add "$log"
git commit -q -m "Release $next"
fi

say "tag and push $next"
git tag -a "$next" -m "jevx $next"
git push -q "$REMOTE" main "$next"

say "waiting for CI"
sha=$(git rev-parse HEAD) # the release commit
for wf in release "Deploy Pages"; do
  id=""
  for _ in $(seq 1 30); do
    id=$(gh run list -R "$REPO" --workflow "$wf" --limit 10 --json databaseId,headSha,headBranch \
         -q "[.[] | select(.headSha==\"$sha\" and .headBranch==\"$next\")][0].databaseId" 2>/dev/null || true)
    [ -n "$id" ] && break; sleep 5
  done
  [ -n "$id" ] || die "no '$wf' run started for $next"
  gh run watch -R "$REPO" "$id" --exit-status >/dev/null || die "'$wf' failed: gh run view -R $REPO $id --log-failed"
  echo "  $wf: ok"
done

say "verify"
n=$(gh release view "$next" -R "$REPO" --json assets -q '.assets | length')
[ "$n" -ge 7 ] || die "release $next has $n assets (want 7)"
echo "  release: $n assets"
for _ in $(seq 1 30); do [ "$(npm view @muthuishere/jevx version 2>/dev/null)" = "${next#v}" ] && break; sleep 10; done
[ "$(npm view @muthuishere/jevx version 2>/dev/null)" = "${next#v}" ] || die "npm does not show ${next#v} yet"
echo "  npm: ${next#v}"
header() { curl -fsS "$1/?nocache=$RANDOM" | grep -o 'version-badge[^>]*>v[0-9.]*' | grep -o 'v[0-9.]*$' | head -1; }
check_site() {
  for _ in $(seq 1 30); do [ "$(header "$1")" = "$next" ] && { echo "  $1: header $next"; return; }; sleep 10; done
  die "$1 header shows $(header "$1"), not $next"
}
check_site "$SITE_URL"
check_mirrors() { # every mirror serves the same installers as the docs site (200, same bytes)
  local f m bad=0
  for f in install.sh install.cmd; do
    want=$(curl -fsS "$SITE_URL/$f?nocache=$RANDOM" | sha256sum)
    for m in ${INSTALLER_MIRRORS:-}; do
      got=$(curl -fsS "$m/$f?nocache=$RANDOM" | sha256sum) || got=unreachable
      if [ "$got" = "$want" ]; then echo "  $m/$f: identical"; else echo "  $m/$f: DRIFT"; bad=1; fi
    done
  done
  return $bad
}
[ -z "${INSTALLER_MIRRORS:-}" ] || check_mirrors || die "an installer mirror differs from $SITE_URL (edge cache is up to 10 min: re-check before acting)"
say "released $next"
