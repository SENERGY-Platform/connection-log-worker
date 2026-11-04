# Plan: outdated MGW modules (SNRGY-5084)

In flight. This file is removed once the change has shipped; the decisions that
outlive it go to `docs/hub-health.md`.

## Goal

A hub owner is notified when a module update on their gateway has been pending for
7 days, the hub list in the web UI shows pending module updates per hub, and the
platform operator is alerted for outdated modules and for gateways that are online
but never send a health report (device-cloud-connector before 0.8.0).

## Chain

The existing health chain is extended, no new path is added:

module-manager → device-cloud-connector (MQTT `health`) → senergy-platform-connector
(Kafka `gateway_health`) → connection-log-worker (Mongo, notification, `/metrics`) →
connection-log (API) → web UI and Prometheus.

| Repository | Change |
|---|---|
| mgw-module-manager | `GET /repository-modules` becomes a shared route so modules reach it under `/restricted`; repositories refresh themselves every 24 h (skipped while a change request is pending or a repository job runs) |
| mgw-device-cloud-connector | the health report carries `module_versions` and `module_versions_error` |
| senergy-platform-connector, connection-log | none, both pass `report` through verbatim |
| connection-log-worker (this repository) | tracking per hub and module, owner notification, two gauges |
| web-ui (`feat/ui-upgrade`) | column "Updates" next to "Health" in the hub list |
| deployment | worker version, alert rules "MGW Modules Outdated" and "MGW Health Not Reported" |

## Report contract

Additive; the report's `version` stays `1`.

```json
{
  "module_versions": [
    {"id": "github.com/SENERGY-Platform/mgw-mqtt-dc", "name": "MQTT Device Connector",
     "source": "<repo source>", "channel": "main", "version": "v1.0.36", "next_version": "v1.0.40"}
  ],
  "module_versions_error": ""
}
```

`next_version` is empty when no update is pending. `module_versions` is `null` with
`module_versions_error` set when the list could not be read.

## This repository

- Collection `hub_module_update_info`, one document per `(hub_id, module_id)`: `name`,
  `version`, `next_version`, `pending_since` (receive time of the first report with
  the update), `notified`.
- Per stored report: `module_versions` unknown changes nothing; a module with
  `next_version` is upserted (`pending_since` and `notified` only on insert); a module
  without `next_version` or missing from the list is deleted. A newer `next_version`
  during a pending period resets nothing, so the owner hears once per period.
- A periodic run claims documents with `notified:false` older than
  `HubModuleOutdatedAge` (default `168h`), grouped by hub, re-reads the hub and sends
  one notification per hub, topic `mgw`. Same claim pattern as the unhealthy
  notification.
- Gauges `connectionlog_worker_hub_modules_outdated{hub_id}` and
  `connectionlog_worker_hub_health_missing{hub_id}`; inactive hubs excluded.

## Decided

- Threshold 7 days; one notification per pending period, bundled per hub.
- Outdated modules do not change the health state.
- The time an update became available is unknown to every component; `pending_since`
  starts with the first report after rollout.

## Rejected

- Module versions as hub attributes with the newest version in the fleet as the
  benchmark: channels and betas make the fleet maximum wrong, the gateway already
  knows its `next_version` per channel, and Prometheus cannot query hub attributes.
