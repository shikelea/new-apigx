# APIGX isolated test deployment

This bundle runs the current APIGX Linux binary with an isolated MySQL 8.2
database and Redis instance. All persistent files live below
`/pan/apigx-test`; only `127.0.0.1:13000` is published on the host.

## Layout

```text
/pan/apigx-test/
  .env
  docker-compose.yml
  Dockerfile.runtime
  apigx-linux-amd64
  BUILD_INFO
  data/
  logs/
  mysql/data/
  redis/data/
  backups/
  verify/
```

The deployment does not reuse any database, Redis instance, network, volume,
or container belonging to another New API installation.

## Build the binary on Windows

Build the frontend first because it is embedded into the Go executable:

```powershell
Set-Location D:\Github\apigx\new-api\web
D:\Github\apigx\.toolchain\bun\bun-windows-x64\bun.exe run build

Set-Location D:\Github\apigx\new-api
$env:GOOS = 'linux'
$env:GOARCH = 'amd64'
$env:CGO_ENABLED = '0'
$env:GOWORK = 'off'
$version = 'v0.0.0-apigx.' + (git rev-parse --short=7 HEAD)
D:\Github\apigx\.toolchain\go\bin\go.exe build -trimpath -ldflags "-s -w -X 'github.com/QuantumNous/new-api/common.Version=$version'" -o D:\Github\apigx\output\apigx-test\apigx-linux-amd64 .
```

Upload the binary and this directory's deployment files to
`/pan/apigx-test`. Generate the real `.env` on the server, never in the
repository. Hexadecimal passwords avoid URL-encoding ambiguity in DSNs.

## Start and inspect

```bash
cd /pan/apigx-test
chmod 600 .env
chmod 755 apigx-linux-amd64
docker compose config --quiet
docker compose up -d --build
docker compose ps
curl --fail --silent http://127.0.0.1:13000/api/status
```

For BaoTa, proxy the chosen HTTPS site to `http://127.0.0.1:13000` and enable
WebSocket forwarding. Keep MySQL and Redis without host port mappings.

## Persistence check

```bash
cd /pan/apigx-test
docker compose restart
docker compose ps
curl --fail --silent http://127.0.0.1:13000/api/status
docker compose exec -T mysql sh -c \
  'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -Nse "SELECT @@default_storage_engine"'
docker compose exec -T redis sh -c \
  'redis-cli -a "$REDIS_PASSWORD" ping 2>/dev/null'
```

The `stub` service is an internal, controllable OpenAI-compatible upstream used
only for routing acceptance. The `verify` service is behind the Compose
`verify` profile and runs the checked-in Stage 2 scripts on the same private
Docker network. Neither service publishes a host port.

## Routing acceptance

Store the verification administrator credentials only in
`/pan/apigx-test/verify/credentials.env` with mode `600`. The scripts read
`APIGX_VERIFY_ROOT_USER` and `APIGX_VERIFY_ROOT_PASSWORD` from that file. Their
session cache and generated fixture state use mode `600` below
`/pan/apigx-test/verify/.toolchain`.

Verify equal Key polling plus in-channel 429/401 failover:

```bash
cd /pan/apigx-test
docker compose --profile verify run --rm verify \
  /work/multi-key-failover.mjs \
  --base http://app:3000 --stub http://stub:8100 \
  --upstream http://stub:8100 --rounds 8
```

Verify quality and capacity routing in two phases. Setup writes a non-secret,
fixture-specific SQL file below `/pan/apigx-test/verify/.toolchain/_state`:

```bash
cd /pan/apigx-test
rm -rf /pan/apigx-test/verify/.toolchain
docker compose --profile verify run --rm verify \
  /work/route-strategy.mjs --phase setup \
  --base http://app:3000 --stub http://stub:8100 \
  --upstream http://stub:8100 --rounds 8

docker compose exec -T mysql sh -lc \
  'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" apigx' \
  < /pan/apigx-test/verify/.toolchain/_state/route-strategy-quality.sql

docker compose --profile verify run --rm verify \
  /work/route-strategy.mjs --phase measure \
  --base http://app:3000 --stub http://stub:8100 \
  --upstream http://stub:8100 --rounds 8
```

Verify the bounded retry budget. With `RetryTimes=1`, one failing request must
try six different Keys in one channel, switch to exactly one other authorized
channel, try six more different Keys, and then return the upstream error. A Key
that returned 429 must remain eligible on later requests:

```bash
cd /pan/apigx-test
docker compose --profile verify run --rm verify \
  /work/bounded-failover-breaker.mjs --phase key-failover \
  --base http://app:3000 --stub http://stub:8100 \
  --upstream http://stub:8100
```

Verify durable breaker recovery in phases so the database can be inspected at
each transition. `breaker-setup` writes the fixture IDs to
`verify/.toolchain/_state/bounded-failover-breaker.json` and must leave the
failed source in state `1` with its base channel automatically disabled:

```bash
cd /pan/apigx-test
rm -f verify/.toolchain/_state/bounded-failover-breaker.json
docker compose --profile verify run --rm verify \
  /work/bounded-failover-breaker.mjs --phase breaker-setup \
  --base http://app:3000 --stub http://stub:8100 \
  --upstream http://stub:8100 --dead-upstream http://127.0.0.1:9
```

Point the fixture's failed base channel at `http://stub:8100`, restart the app,
and confirm the probe row survives. Use the checked-in helper to make the stub
return 401, set that row's `next_probe_at` to the current Unix time, and wait for
the 15-second System Task. State `3` must have `next_probe_at=0`; waiting another
20 seconds must not change `probe_count`. Probe requests increment
`apigx_quality_buckets.probe_count` and must not add rows to `logs`.

```bash
docker compose --profile verify run --rm --no-deps verify \
  /work/stub-profile.mjs --status 401
```

Restore the stub, rotate the fixture Key, and observe one successful probe in
state `1` with `consecutive_success=1`. User traffic must still use only the
healthy fallback. The second successful probe changes the row to state `4` and
re-enables the base channel:

```bash
docker compose --profile verify run --rm --no-deps verify \
  /work/stub-profile.mjs --status 0
docker compose --profile verify run --rm verify \
  /work/bounded-failover-breaker.mjs --phase rotate-breaker-key \
  --base http://app:3000 --stub http://stub:8100
docker compose --profile verify run --rm verify \
  /work/bounded-failover-breaker.mjs --phase blocked-traffic \
  --base http://app:3000 --stub http://stub:8100
docker compose --profile verify run --rm verify \
  /work/bounded-failover-breaker.mjs --phase recovered \
  --base http://app:3000 --stub http://stub:8100
```
