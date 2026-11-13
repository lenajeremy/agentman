#!/bin/bash
# A Claude Stop hook for a test agent, as Claude sends one when its turn
# ends: stop-hook.sh <session uuid> <folder name> <last words>.
T=${TMPDIR%/}/amx
TOKEN=$(python3 -c "import json;print(json.load(open('$T/home/.agentman/config.json'))['token'])")
curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -H "X-Agentman-Token: $TOKEN" \
  --data "{\"hook_event_name\":\"Stop\",\"session_id\":\"$1\",\"cwd\":\"$T/home/projects/$2\",\"last_assistant_message\":\"$3\"}" \
  http://127.0.0.1:18797/hook/claude/Stop
