# unifi-statsd-exporter
A simple statsd exporter to keep track of power usage from unifi devices/pdus

Polls a UniFi OS console (UDM / UDM SE / UCG / Cloud Key Gen2+) for device,
PoE port, and PDU outlet power, and emits it as DogStatsD metrics. Optionally
receives SNMP v1/v2c traps from UniFi devices and forwards them as DogStatsD
events and counters.

## Configuration

Copy `config.example.yaml` and run with `-configFile=<path>`. Secrets can be
supplied via environment variables instead of the file:

| Env var | Config key |
|---|---|
| `UNIFI_URL` | `unifi.url` |
| `UNIFI_SITE` | `unifi.site` |
| `UNIFI_API_KEY` | `unifi.apiKey` (preferred) |
| `UNIFI_USERNAME` / `UNIFI_PASSWORD` | `unifi.username` / `unifi.password` (local account) |
| `STATSD_ADDRESS` | `statsd.address` |
| `SNMP_TRAP_COMMUNITY` | `snmpTraps.community` |

SNMP traps are received on `snmpTraps.listenAddress` (default `0.0.0.0:1162`,
unprivileged); map port 162 to it at the Kubernetes Service.

## Metrics

All names are prefixed with `statsd.namespace` (default `unifi.`). Every
device metric is tagged `site`, `device`, `mac`, `model`, `device_type`.

| Metric | Extra tags | Notes |
|---|---|---|
| `device.online` | | 1 when connected |
| `device.poe.budget_watts`, `device.poe.used_watts` | | switches / UDM |
| `device.poe.ports_active` | | ports delivering PoE |
| `device.outlet.power_watts`, `device.outlet.budget_watts` | | PDUs |
| `poe.power_watts`, `poe.voltage_volts`, `poe.current_amps` | `port`, `port_name`, `poe_mode`, `poe_class`, `connected`, `connected_type` | PoE-capable ports only |
| `poe.enabled`, `poe.good`, `port.up` | same | 1/0 |
| `poe.connected_clients` | same | wired clients seen on the port |
| `pdu.outlet.power_watts`, `pdu.outlet.voltage_volts`, `pdu.outlet.current_amps`, `pdu.outlet.power_factor` | `outlet`, `outlet_name` | metered outlets only |
| `pdu.outlet.relay_on` | same | 1/0 |
| `site.poe.used_watts`, `site.outlet.power_watts`, `site.devices` | `site` only | totals |
| `collector.poll` (count), `collector.poll_duration_ms` (timing) | `result` | exporter health |
| `snmp.trap` (count) + event | `source`, `trap`, `oid` | when traps are enabled |

## Building and releasing

```sh
make build          # bin/unifi-statsd-exporter
make test           # go test ./...  (make test.race needs gcc)
make docker.build   # local image tagged YYYY.MM.DD-HHMM and latest
make docker.push    # multi-arch (amd64/arm64) buildx build + push to Docker Hub
make release        # test + docker.push
```

Image tags are UTC date-coded, e.g. `jgulick48/unifi-statsd-exporter:2026.10.31-1542`.
Override with `IMAGE=...`, `VERSION=...`, or `PLATFORMS=...`.
