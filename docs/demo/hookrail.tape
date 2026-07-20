# VHS tape → docs/demo/hookrail-demo.gif
# Records the send → 202 → delivered flow against the quickstart stack.
#
# One-time:   brew install vhs        (or: go install github.com/charmbracelet/vhs@latest)
# Record:
#   export HOOKRAIL_MASTER_KEY=$(openssl rand -hex 32)
#   docker compose -f deploy/compose/quickstart.yml up -d
#   sleep 30                                  # let migrate + services come up
#   vhs docs/demo/hookrail.tape               # writes docs/demo/hookrail-demo.gif
#   docker compose -f deploy/compose/quickstart.yml down -v
#
# NOTE: `hookrail-ctl seed` prints `producer_key=...`; if your field name differs,
# adjust the awk filter below. Tune Sleep values to your machine's timing.

Output docs/demo/hookrail-demo.gif

Set Shell "bash"
Set FontSize 18
Set Width 1200
Set Height 720
Set Padding 20
Set TypingSpeed 55ms
Set Theme "Catppuccin Mocha"

Type "# hookrail: POST an event, watch it get delivered"
Enter
Sleep 1s

# Mint a producer key + a subscription pointed at the bundled receiver.
Type "eval $(docker compose -f deploy/compose/quickstart.yml run --rm -T api \"
Enter
Type "  hookrail-ctl seed -url http://test-receiver:9090/succeed -topic demo.hello \"
Enter
Type "  | tee /dev/stderr | sed -n 's/^producer_key=/export PK=/p')"
Enter
Sleep 4s

Type "curl -sS -X POST localhost:8080/v1/events \"
Enter
Type "  -H \"Authorization: Bearer $PK\" -H 'Content-Type: application/json' \"
Enter
Type "  -d '{\"topic\":\"demo.hello\",\"payload\":{\"msg\":\"hi\"}}' | jq ."
Enter
Sleep 3s

Type "# -> 202 { event_id, delivery_ids }  — accepted, durably queued"
Enter
Sleep 2s

# Grab the delivery id from a fresh send and confirm it arrived.
Type "DID=$(curl -sS -X POST localhost:8080/v1/events -H \"Authorization: Bearer $PK\" \"
Enter
Type "  -H 'Content-Type: application/json' -d '{\"topic\":\"demo.hello\",\"payload\":{\"n\":2}}' \"
Enter
Type "  | jq -r '.delivery_ids[0]')"
Enter
Sleep 3s

Type "sleep 1; curl -s \"localhost:9090/received?delivery_id=$DID\""
Enter
Sleep 3s

Type "# receipt count -> 1  ✅ delivered, signed, at-least-once"
Enter
Sleep 2s

Type "# Retries & dead-letters are running live at http://localhost:8085"
Enter
Sleep 3s
