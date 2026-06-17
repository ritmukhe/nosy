#!/usr/bin/env bash
#
# nosy showcase — runs the demo query sequence against the clab-nosy-demo
# fabric with narration before each query. Suitable for a live NANOG/RIPE demo.
#
# Prereq: the fabric is deployed (see README.md) and `nosy` is on PATH.
# Override the binary with NOSY=./nosy ./demo.sh if it isn't installed.

set -uo pipefail

NOSY="${NOSY:-nosy}"

bold() { printf '\033[1m%s\033[0m\n' "$1"; }
rule() { printf '%s\n' "────────────────────────────────────────────────────────────"; }

step() {
	echo
	rule
	bold "$1"
	echo "$2"
	rule
	# Pause for the audience; skip with NONINTERACTIVE=1.
	if [[ "${NONINTERACTIVE:-0}" != "1" ]]; then
		read -rp "↵ to run: $3 "
	else
		echo "\$ $3"
	fi
	eval "$3"
}

bold "nosy — poke around, find answers."
echo "No YANG paths. No model digging. Just intent → live device state."

step \
	"1. BGP neighbors on spine1" \
	"nosy detects the NOS and firmware, resolves the 'bgp.neighbors' intent to
the right YANG paths for that release, validates them against the schema, then
GETs and renders the session table — the operator typed no paths." \
	"$NOSY query --target clab-nosy-demo-spine1 --password NokiaSrl1! --intent bgp.neighbors"

step \
	"2. Interface counters on leaf1 (natural language)" \
	"Same pipeline, but the operator just describes what they want. nosy matches
the phrase to a curated intent — no need to know the intent's exact name." \
	"$NOSY query --target clab-nosy-demo-leaf1 --password NokiaSrl1! \"show interface counters\""

step \
	"3. BGP sessions as JSON for scripting" \
	"The same query in a stable, versioned JSON envelope (ADR-003) — pipe it into
jq and build automation on top, with no screen-scraping." \
	"$NOSY query --target clab-nosy-demo-spine1 --password NokiaSrl1! --intent bgp.neighbors --output json"

step \
	"4. LLDP neighbors on spine1" \
	"A different intent, same flow. nosy maps 'system.lldp' to the LLDP neighbor
paths for this release — discover the topology without knowing the model tree." \
	"$NOSY query --target clab-nosy-demo-spine1 --password NokiaSrl1! --intent system.lldp"

step \
	"5. Interface status across leaf1" \
	"One more intent: admin/oper state, description, MTU and speed per port — the
'show interfaces' an operator reaches for first, with zero YANG knowledge." \
	"$NOSY query --target clab-nosy-demo-leaf1 --password NokiaSrl1! --intent interface.status"

echo
bold "Done. One tool, any NOS, no YANG."
