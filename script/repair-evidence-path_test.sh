#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
PATH_TOOL="$ROOT/script/repair-evidence-path.sh"
TMP_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/repair-evidence-path-test.XXXXXX")

cleanup() {
	rm -rf -- "$TMP_ROOT"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

fail() {
	printf 'repair-evidence-path_test: FAIL: %s\n' "$*" >&2
	exit 1
}

repo="$TMP_ROOT/repo"
git init -q -b main "$repo"
git -C "$repo" config user.email repair-evidence-test@beamfall.invalid
git -C "$repo" config user.name repair-evidence-test
printf '.agent-evidence/\n' >"$repo/.gitignore"
git -C "$repo" add .gitignore
git -C "$repo" commit -qm initial
git -C "$repo" branch lane-a
git -C "$repo" branch lane-b

write_round() {
	ticket=$1
	round=$2
	summary_path=$("$PATH_TOOL" "$ticket" "$round" summary)
	planted_red_path=$("$PATH_TOOL" "$ticket" "$round" planted-red)

	[ "$summary_path" = ".agent-evidence/$ticket-repair-r$round-summary.txt" ] \
		|| fail "wrong summary path for $ticket: $summary_path"
	[ "$planted_red_path" = ".agent-evidence/$ticket-repair-r$round-planted-red.txt" ] \
		|| fail "wrong planted-red path for $ticket: $planted_red_path"

	mkdir -p "$repo/.agent-evidence"
	printf '%s\nsummary\n' "$ticket" >"$repo/$summary_path"
	printf '%s\nplanted red\n' "$ticket" >"$repo/$planted_red_path"
	git -C "$repo" add -f -- "$summary_path" "$planted_red_path"
	git -C "$repo" commit -qm "$ticket repair evidence"
}

git -C "$repo" switch -q lane-a
write_round TICKET-A 2
git -C "$repo" switch -q main
git -C "$repo" merge -q --ff-only lane-a

git -C "$repo" switch -q lane-b
write_round TICKET-B 2
git -C "$repo" rebase -q main || fail "lane-b evidence conflicted while rebasing over lane-a"
git -C "$repo" switch -q main
git -C "$repo" merge -q --ff-only lane-b

assert_ticket_first_line() {
	ticket=$1
	path=$2
	first_line=$(sed -n '1p' "$repo/$path")
	[ "$first_line" = "$ticket" ] || fail "$path belongs to $first_line, expected $ticket"
}

assert_ticket_first_line TICKET-A .agent-evidence/TICKET-A-repair-r2-summary.txt
assert_ticket_first_line TICKET-A .agent-evidence/TICKET-A-repair-r2-planted-red.txt
assert_ticket_first_line TICKET-B .agent-evidence/TICKET-B-repair-r2-summary.txt
assert_ticket_first_line TICKET-B .agent-evidence/TICKET-B-repair-r2-planted-red.txt

[ ! -e "$repo/.agent-evidence/repair-r2-summary.txt" ] \
	|| fail "generic repair-r2-summary.txt was produced"
[ ! -e "$repo/.agent-evidence/repair-r2-planted-red.txt" ] \
	|| fail "generic repair-r2-planted-red.txt was produced"

if rg -n --glob '!**/*_test.*' '\.agent-evidence/repair-r' "$ROOT/script"; then
	fail "a production tooling path still names generic repair evidence"
fi

printf 'repair-evidence-path_test: PASS\n'
