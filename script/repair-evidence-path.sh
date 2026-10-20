#!/bin/sh
set -eu

usage() {
	printf 'usage: %s TICKET ROUND KIND\n' "$(basename "$0")" >&2
	exit 2
}

[ "$#" -eq 3 ] || usage

ticket=$1
round=$2
kind=$3

case "$ticket" in
	''|[._-]*|*[!A-Za-z0-9._-]*) usage ;;
esac
case "$round" in
	''|*[!0-9]*) usage ;;
esac
[ "$round" -gt 0 ] 2>/dev/null || usage
case "$kind" in
	''|[-._]*|*[!A-Za-z0-9._-]*) usage ;;
esac

printf '.agent-evidence/%s-repair-r%s-%s.txt\n' "$ticket" "$round" "$kind"
