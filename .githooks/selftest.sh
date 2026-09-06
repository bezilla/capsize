#!/usr/bin/env bash
#
# selftest: proves the pre-push gate rejects what it claims to reject, and --
# just as important -- accepts what it claims to accept.
#
# A gate nobody has watched fail is a gate nobody knows works, and a gate only
# ever watched to refuse could be one that refuses everything. Each case builds a
# throwaway repository, produces exactly one kind of history, and feeds the hook
# the same stdin git would feed it on a real push:
#
#     <local ref> <local sha> <remote ref> <remote sha>
#
# Two of these cases exist to hold a decision in place rather than to catch a
# bug: the address is asserted exactly, the name is checked against a short list,
# and both halves of that are asserted so a future "tidy-up" fails a test instead
# of making this repository unpushable. See DESIGN.md.
#
# Every case captures the hook's status with `|| rc=$?` rather than running it
# bare and reading `$?`. CI runs its steps under `bash -eo pipefail`, where a
# bare non-zero command kills the step before the assertion is reached, so the
# other shape reports nothing on exactly the cases it exists to prove.

set -uo pipefail

HOOK="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/pre-push"
ZERO='0000000000000000000000000000000000000000'
CANON_NAME='Paul Bezilla'
CANON_EMAIL='bezilla@protonmail.com'
CANON="${CANON_NAME} <${CANON_EMAIL}>"
HISTORICAL_NAME='pjbezilla'

pass=0
fail=0

ok()  { printf '  \033[32mok\033[0m   %-58s rc=%s\n' "$1" "${2:-0}"; pass=$((pass + 1)); }
bad() { printf '  \033[31mFAIL\033[0m %-58s rc=%s\n' "$1" "${2:-?}"; fail=$((fail + 1)); }

# Build a throwaway repo with one clean commit. Echoes its path.
new_repo() {
	local d
	d="$(mktemp -d)"
	git -C "$d" init -q -b main
	git -C "$d" config user.name  "$CANON_NAME"
	git -C "$d" config user.email "$CANON_EMAIL"
	printf 'clean\n' > "$d/README.md"
	git -C "$d" add -- README.md
	git -C "$d" commit -q -m 'Base commit'
	printf '%s' "$d"
}

commit_msg() {
	local d="$1" msg="$2"
	printf 'x %s\n' "$RANDOM" > "$d/file.txt"
	git -C "$d" add -- file.txt
	git -C "$d" commit -q -m "$msg"
}

# run_hook <repo> <remote_sha>  -> echoes exit status
run_hook() {
	local d="$1" base="$2" tip rc=0
	tip="$(git -C "$d" rev-parse HEAD)"
	( cd "$d" && printf 'refs/heads/main %s refs/heads/main %s\n' "$tip" "$base" \
		| "$HOOK" origin >/dev/null 2>&1 ) || rc=$?
	printf '%s' "$rc"
}

# run_hook_tag <repo> <tag> -> echoes exit status
run_hook_tag() {
	local d="$1" tag="$2" obj rc=0
	obj="$(git -C "$d" rev-parse "refs/tags/${tag}")"
	( cd "$d" && printf 'refs/tags/%s %s refs/tags/%s %s\n' "$tag" "$obj" "$tag" "$ZERO" \
		| "$HOOK" origin >/dev/null 2>&1 ) || rc=$?
	printf '%s' "$rc"
}

# --- 1: canonical identity passes ---------------------------------------------
d="$(new_repo)"; base="$(git -C "$d" rev-parse HEAD)"
commit_msg "$d" 'Add a file'
rc="$(run_hook "$d" "$base")"
[ "$rc" = '0' ] && ok 'canonical identity, clean commit: accepted' "$rc" \
                || bad 'canonical identity was REJECTED -- the gate blocks good history' "$rc"
rm -rf "$d"

# --- 2: wrong author address --------------------------------------------------
d="$(new_repo)"; base="$(git -C "$d" rev-parse HEAD)"
printf 'x\n' > "$d/file.txt"; git -C "$d" add -- file.txt
GIT_AUTHOR_NAME='Somebody Else' GIT_AUTHOR_EMAIL='somebody@example.invalid' \
	git -C "$d" commit -q -m 'Wrong author'
rc="$(run_hook "$d" "$base")"
[ "$rc" != '0' ] && ok 'wrong author address: rejected' "$rc" || bad 'wrong AUTHOR was accepted' "$rc"
rm -rf "$d"

# --- 3: wrong committer address -----------------------------------------------
d="$(new_repo)"; base="$(git -C "$d" rev-parse HEAD)"
printf 'x\n' > "$d/file.txt"; git -C "$d" add -- file.txt
GIT_COMMITTER_NAME='Some Service' GIT_COMMITTER_EMAIL='noreply@example.invalid' \
	git -C "$d" commit -q -m 'Wrong committer'
rc="$(run_hook "$d" "$base")"
[ "$rc" != '0' ] && ok 'wrong committer address: rejected' "$rc" || bad 'wrong COMMITTER was accepted' "$rc"
rm -rf "$d"

# --- 4: the canonical NAME on a wrong address ---------------------------------
# The name list is not a way in. The address is the identity, so the right name
# beside the wrong address is still refused.
d="$(new_repo)"; base="$(git -C "$d" rev-parse HEAD)"
printf 'x\n' > "$d/file.txt"; git -C "$d" add -- file.txt
GIT_AUTHOR_NAME="$CANON_NAME" GIT_AUTHOR_EMAIL='paul@example.invalid' \
GIT_COMMITTER_NAME="$CANON_NAME" GIT_COMMITTER_EMAIL='paul@example.invalid' \
	git -C "$d" commit -q -m 'Canonical name, wrong address'
rc="$(run_hook "$d" "$base")"
[ "$rc" != '0' ] && ok 'canonical name on a wrong address: rejected' "$rc" \
                 || bad 'the canonical NAME on a WRONG ADDRESS was accepted' "$rc"
rm -rf "$d"

# --- 5: canonical address, name not on the accepted list ----------------------
# This repository asserts the address exactly and checks the name against a short
# list, because three published commits carry a second spelling. That is not a
# licence for any name: one nobody here has used is still rejected.
d="$(new_repo)"; base="$(git -C "$d" rev-parse HEAD)"
printf 'x\n' > "$d/file.txt"; git -C "$d" add -- file.txt
GIT_AUTHOR_NAME='Someone Unlisted' GIT_AUTHOR_EMAIL="$CANON_EMAIL" \
GIT_COMMITTER_NAME='Someone Unlisted' GIT_COMMITTER_EMAIL="$CANON_EMAIL" \
	git -C "$d" commit -q -m 'Unlisted name on the canonical address'
rc="$(run_hook "$d" "$base")"
[ "$rc" != '0' ] && ok 'unlisted name on the canonical address: rejected' "$rc" \
                 || bad 'an UNLISTED NAME on the canonical address was accepted' "$rc"
rm -rf "$d"

# --- 6: the historical name variant is accepted -------------------------------
# The other half of the same decision. v0.1.0 and v0.1.1 are reachable from
# commits made under this name; if the gate rejected it the repository could not
# be pushed at all without rewriting the tags and destroying the release
# binaries. This case fails if someone "tightens" the list back to one entry.
d="$(new_repo)"; base="$(git -C "$d" rev-parse HEAD)"
printf 'x\n' > "$d/file.txt"; git -C "$d" add -- file.txt
GIT_AUTHOR_NAME="$HISTORICAL_NAME" GIT_AUTHOR_EMAIL="$CANON_EMAIL" \
GIT_COMMITTER_NAME="$HISTORICAL_NAME" GIT_COMMITTER_EMAIL="$CANON_EMAIL" \
	git -C "$d" commit -q -m 'Historical name variant'
rc="$(run_hook "$d" "$base")"
[ "$rc" = '0' ] && ok 'historical name variant on the canonical address: accepted' "$rc" \
                || bad 'historical name variant was REJECTED -- v0.1.x would be unpushable' "$rc"
rm -rf "$d"

# --- 7: a trailer key outside the allowlist -----------------------------------
d="$(new_repo)"; base="$(git -C "$d" rev-parse HEAD)"
commit_msg "$d" 'Add a file

Reviewed-by: Someone <someone@example.invalid>'
rc="$(run_hook "$d" "$base")"
[ "$rc" != '0' ] && ok 'disallowed trailer key: rejected' "$rc" \
                 || bad 'a trailer OUTSIDE the allowlist was accepted' "$rc"
rm -rf "$d"

# --- 8: Signed-off-by, canonical ----------------------------------------------
d="$(new_repo)"; base="$(git -C "$d" rev-parse HEAD)"
commit_msg "$d" "Add a file

Signed-off-by: ${CANON}"
rc="$(run_hook "$d" "$base")"
[ "$rc" = '0' ] && ok 'Signed-off-by, canonical identity: accepted' "$rc" \
                || bad 'the permitted sign-off was REJECTED' "$rc"
rm -rf "$d"

# --- 9: Signed-off-by under the HISTORICAL name is refused --------------------
# The name allowance covers the author field on commits that already exist. A
# sign-off is written deliberately, today, so it gets the exact identity.
d="$(new_repo)"; base="$(git -C "$d" rev-parse HEAD)"
commit_msg "$d" "Add a file

Signed-off-by: ${HISTORICAL_NAME} <${CANON_EMAIL}>"
rc="$(run_hook "$d" "$base")"
[ "$rc" != '0' ] && ok 'Signed-off-by under the historical name: rejected' "$rc" \
                 || bad 'a sign-off under the historical name was accepted' "$rc"
rm -rf "$d"

# --- 10: Verified and Measured take free text ---------------------------------
d="$(new_repo)"; base="$(git -C "$d" rev-parse HEAD)"
commit_msg "$d" 'Add a file

Verified: read-only guard passes on all packages.'
rc="$(run_hook "$d" "$base")"
[ "$rc" = '0' ] && ok 'Verified, free text: accepted' "$rc" || bad 'Verified was REJECTED' "$rc"
rm -rf "$d"

d="$(new_repo)"; base="$(git -C "$d" rev-parse HEAD)"
commit_msg "$d" 'Add a file

Measured: 3 runs, 0 failures.'
rc="$(run_hook "$d" "$base")"
[ "$rc" = '0' ] && ok 'Measured, free text: accepted' "$rc" || bad 'Measured was REJECTED' "$rc"
rm -rf "$d"

# --- 11: an unlisted evidence key is refused ----------------------------------
d="$(new_repo)"; base="$(git -C "$d" rev-parse HEAD)"
commit_msg "$d" 'Add a file

Tested: every package green.'
rc="$(run_hook "$d" "$base")"
[ "$rc" != '0' ] && ok 'unlisted evidence key (Tested): rejected' "$rc" \
                 || bad 'an unlisted evidence key was accepted' "$rc"
rm -rf "$d"

# --- 12: a mid-message Key: Value line is not a trailer -----------------------
d="$(new_repo)"; base="$(git -C "$d" rev-parse HEAD)"
commit_msg "$d" 'Add a file

Verified: this line is not in the final paragraph.

So it is prose, and this paragraph is what makes it so.'
rc="$(run_hook "$d" "$base")"
[ "$rc" = '0' ] && ok 'mid-message Key: Value, not a trailer: accepted' "$rc" \
                || bad 'ordinary prose was treated as a trailer and REJECTED' "$rc"
rm -rf "$d"

# --- 13: annotated tags -------------------------------------------------------
d="$(new_repo)"
GIT_COMMITTER_NAME='Some Service' GIT_COMMITTER_EMAIL='noreply@example.invalid' \
	git -C "$d" tag -a v9.9.9 -m 'Release nine'
rc="$(run_hook_tag "$d" 'v9.9.9')"
[ "$rc" != '0' ] && ok 'annotated tag, wrong tagger: rejected' "$rc" \
                 || bad 'a tag tagged by somebody else was accepted' "$rc"
rm -rf "$d"

d="$(new_repo)"
git -C "$d" tag -a v1.0.0 -m 'Release one'
rc="$(run_hook_tag "$d" 'v1.0.0')"
[ "$rc" = '0' ] && ok 'annotated tag, canonical tagger: accepted' "$rc" \
                || bad 'a correctly tagged release was REJECTED' "$rc"
rm -rf "$d"

# The tagger goes through the same name list as a commit, for the same reason:
# v0.1.0 and v0.1.1 are exactly the tags the allowance exists to protect.
d="$(new_repo)"
GIT_COMMITTER_NAME="$HISTORICAL_NAME" GIT_COMMITTER_EMAIL="$CANON_EMAIL" \
	git -C "$d" tag -a v1.1.0 -m 'Release one point one'
rc="$(run_hook_tag "$d" 'v1.1.0')"
[ "$rc" = '0' ] && ok 'annotated tag, historical tagger name: accepted' "$rc" \
                || bad 'the historical tagger name was REJECTED -- v0.1.x tags would fail' "$rc"
rm -rf "$d"

d="$(new_repo)"
git -C "$d" tag -a v2.0.0 -m 'Release two

Reviewed-by: Someone <someone@example.invalid>'
rc="$(run_hook_tag "$d" 'v2.0.0')"
[ "$rc" != '0' ] && ok 'disallowed trailer in a tag annotation: rejected' "$rc" \
                 || bad 'a tag annotation carried a disallowed trailer' "$rc"
rm -rf "$d"

printf '\n%d as expected, %d unexpected\n' "$pass" "$fail"
[ "$fail" -eq 0 ] || exit 1
