#!/bin/bash -eu

port="$(snapctl get http.port)"
host="$(snapctl get http.host)"

echo "Starting inference.d on $host:$port"
exec "$SNAP"/bin/inference_d --port "$port" --host "$host"
