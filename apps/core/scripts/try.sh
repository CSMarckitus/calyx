#!/usr/bin/env bash
# Calls the gRPC API of a running Go core, for manual checks.
#
# Requirements: grpcurl (go install github.com/fullstorydev/grpcurl/cmd/grpcurl@v1.9.3), jq,
# and qrencode for "pair-qr".
#
# Environment:
#   CALYX_ADDR       address of the core (default 127.0.0.1:50051)
#   GRPC_AUTH_TOKEN  required, the same token as the core; sent as "authorization: Bearer <token>"
#
# WARNING: "pair-code", "pair-qr" and "send" use a real WhatsApp account. Use a test number.
# The WhatsApp Web provider is against WhatsApp's Terms of Service and the number can be banned.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
ADDR="${CALYX_ADDR:-127.0.0.1:50051}"

usage() {
	cat <<'EOF'
Usage: apps/core/scripts/try.sh <command> [arguments]

Commands that do not use WhatsApp:
  list                          List the instances.
  status <name>                 Show the status of one instance.
  events [name]                 Stream the events (of one instance, or of all). Stop with Ctrl+C.

Commands that use a real WhatsApp account:
  pair-code <name> <phone> [locale] [identity]
                                Create an instance and pair it with a pairing code.
                                <phone>: digits with country code, for example 34600111222.
  pair-qr <name> [locale] [identity]
                                Create an instance and pair it with a QR shown in the terminal.

  [locale]: for example es-ES. A new instance needs it; a re-pair keeps the stored one.
  [identity]: CHROME_WINDOWS, EDGE_WINDOWS, FIREFOX_WINDOWS, FIREFOX_LINUX or SAFARI_MAC_OS.
              Without it, the core picks one at random.
  send <name> <to> <text>       Send one text message.
  remove <name>                 Remove the instance and unlink the device.
EOF
	exit 1
}

need() {
	command -v "$1" >/dev/null || { echo "Install $1 first." >&2; exit 1; }
}

# call <service/method> <json>
call() {
	local args=(-plaintext -import-path "$ROOT/packages/shared-types/proto"
		-proto calyx/core/v1/instance.proto -proto calyx/core/v1/message.proto -proto calyx/core/v1/event.proto)
	[[ -n "${GRPC_AUTH_TOKEN:-}" ]] || { echo "Set GRPC_AUTH_TOKEN to the token of the core." >&2; exit 1; }
	args+=(-H "authorization: Bearer $GRPC_AUTH_TOKEN")
	grpcurl "${args[@]}" -d "$2" "$ADDR" "calyx.core.v1.$1"
}

# whatsapp_web <pairing json> [locale] [identity]: the whatsapp_web settings of CreateInstance.
whatsapp_web() {
	jq -n --argjson pairing "$1" --arg l "${2:-}" --arg i "${3:-}" \
		'$pairing + (if $l != "" then {locale: $l} else {} end)
			+ (if $i != "" then {device_identity: ("DEVICE_IDENTITY_" + $i)} else {} end)'
}

# confirm <question>: stops the script unless the answer is "y".
confirm() {
	read -r -p "$1 [y/N] " answer
	[[ "$answer" == "y" ]] || { echo "Stopped."; exit 1; }
}

need grpcurl
need jq
[[ $# -ge 1 ]] || usage
command="$1"
shift

case "$command" in
list)
	call InstanceService/ListInstances '{}'
	;;
status)
	[[ $# -eq 1 ]] || usage
	call InstanceService/GetInstanceStatus "$(jq -n --arg n "$1" '{instance_name: $n}')"
	;;
events)
	if [[ $# -eq 1 ]]; then
		call EventService/Subscribe "$(jq -n --arg n "$1" '{instance_names: [$n]}')"
	else
		call EventService/Subscribe '{}'
	fi
	;;
pair-code)
	[[ $# -ge 2 && $# -le 4 ]] || usage
	confirm "Pair $2 as a linked device? Use a test number."
	echo "On the phone: WhatsApp > Linked devices > Link with phone number. Enter the code below."
	settings="$(whatsapp_web "$(jq -n --arg p "$2" '{pairing_code: {phone_number: $p}}')" "${3:-}" "${4:-}")"
	call InstanceService/CreateInstance "$(jq -n --arg n "$1" --argjson s "$settings" '{instance_name: $n, whatsapp_web: $s}')"
	;;
pair-qr)
	[[ $# -ge 1 && $# -le 3 ]] || usage
	need qrencode
	confirm "Pair a WhatsApp account as a linked device? Use a test number."
	echo "On the phone: WhatsApp > Linked devices > Link a device. Scan the last QR shown."
	# The core sends the QR as text (decision still open), so it is drawn here.
	settings="$(whatsapp_web '{"pairing_qr": true}' "${2:-}" "${3:-}")"
	call InstanceService/CreateInstance "$(jq -n --arg n "$1" --argjson s "$settings" '{instance_name: $n, whatsapp_web: $s}')" |
		jq --unbuffered -c '.' |
		while read -r update; do
			code="$(jq -r '.pairingCode // empty' <<<"$update")"
			if [[ -n "$code" ]]; then
				qrencode -t ansiutf8 "$code"
			else
				echo "$update"
			fi
		done
	;;
send)
	[[ $# -eq 3 ]] || usage
	confirm "Send \"$3\" from $1 to $2?"
	call MessageService/SendPlainText "$(jq -n --arg n "$1" --arg t "$2" --arg x "$3" \
		'{instance_name: $n, to: $t, text: $x}')"
	;;
remove)
	[[ $# -eq 1 ]] || usage
	confirm "Remove $1 and unlink its device?"
	call InstanceService/RemoveInstance "$(jq -n --arg n "$1" '{instance_name: $n}')"
	;;
*)
	usage
	;;
esac
