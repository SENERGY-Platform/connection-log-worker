# Hub health

The worker stores the latest health report per hub from Kafka `gateway_health`
and notifies the owner when a hub stays unhealthy. connection-log serves the
stored reports. The message format is defined by senergy-platform-connector
(`docs/health-reports.md` there), the report itself by
mgw-device-cloud-connector.

## Applies when

You change the health consumer, the `gatewayhealth` document, the unhealthy
notification, the module tracking, the metrics, or the handling of inactive hubs. connection-log reads `gatewayhealth` with a read-only Mongo user,
so a changed field name breaks its API.

## Malformed and unstorable reports are skipped

Reports come from gateways. A listener error ends in the consumer's retry path
and finally `log.Fatal`, so one bad report would stop the worker for everyone.
The health listeners therefore log and skip:

- messages that do not parse, lack `hub_id`, have an unparseable `time`, or a
  `report.state` outside 0 to 2;
- reports nested deeper than 50 levels or with a NUL in a key, which BSON or
  MongoDB cannot store;
- documents MongoDB rejects on write.

A skipped report is replaced by the next heartbeat, at the latest after five
minutes. Connection errors to MongoDB are still returned.

## Storage

One document per hub in `HUB_HEALTH_COLLECTION` (`gatewayhealth`):
`hub_id` (unique index), `state`, `reasons`, `report`, `report_time`,
`received`. A report only replaces the stored one if its `received` is not
older, so out-of-order delivery cannot overwrite a newer state. In batch mode
only the newest message per hub is written. Inactive hubs are kept in
`HUB_INACTIVE_COLLECTION` (`hub_inactive`), one document `{hub_id}` each.

## Unhealthy notification

Tracking documents in `HUB_UNHEALTHY_NOTIFICATION_INFO_COLLECTION`:

| Stored state | Tracking |
|---|---|
| `2` | created if absent, with `unhealthy_since` |
| `1` | removed, no recovery notification |
| `0` | unchanged |

Every `HUB_UNHEALTHY_NOTIFICATION_INTERVAL` the worker claims tracking documents
older than `HUB_UNHEALTHY_NOTIFICATION_DELAY` (10m) and notifies the hub owner
once, topic `mgw`, title "Network Unhealthy", with the reasons. Before sending
it re-reads the stored state and drops the tracking if the hub is no longer
unhealthy. Deleting a hub removes both documents. For an inactive hub the notification is
not sent, see below.

## Outdated modules

The report carries `module_versions`: per module `id`, `name`, `version` and
`next_version`, the latter empty if no update is pending. The worker tracks pending
updates in `HUB_MODULE_UPDATE_INFO_COLLECTION` (`hub_module_update_info`), one
document per `(hub_id, module_id)` (unique index): `name`, `version`,
`next_version`, `pending_since` (unix seconds), `notified`. Tracking changes only
for a report that the health store accepted; an older report rejected by the
`received` filter changes nothing.

| `module_versions` in the report | Tracking |
|---|---|
| absent, `null` or malformed | unchanged; the report is stored anyway |
| module with `next_version` | created if absent with `pending_since` = receive time of the report and `notified=false`; name, version and `next_version` are updated otherwise |
| module without `next_version`, or no longer listed | removed |

Entries without `id` are skipped. A newer `next_version` during a pending period
changes neither `pending_since` nor `notified`, so the owner hears once per pending
period. No component knows when an update became available: `pending_since`
starts with the first report after rollout, the first notifications therefore
come `HUB_MODULE_OUTDATED_AGE` after the rollout at the earliest.

Every `HUB_MODULE_OUTDATED_NOTIFICATION_INTERVAL` the worker selects the
documents with `notified=false` and `pending_since` older than
`HUB_MODULE_OUTDATED_AGE` (168h), grouped by hub. Per hub it claims each document
(`notified` false to true on exactly the selected `pending_since`, so one worker
wins), reads the claimed documents and the hub again and sends one notification
for all claimed modules: topic `mgw`, title "Network Modules Outdated", the module
list cut at 1000 characters. Unknown hub: tracking is removed. Inactive hub or hub
without owner: nothing is sent, the claim stays. Any other read error and a failed
send release the claim for exactly the claimed documents. Deleting a hub removes its
documents.

## Metrics

With `PROMETHEUS_PORT` set, `/metrics` serves five gauges, read from
`gatewayhealth`, `HUB_MODULE_UPDATE_INFO_COLLECTION`, the hub states and
`HUB_INACTIVE_COLLECTION` on every scrape:

- `connectionlog_worker_hub_health_state{hub_id}`: the stored state, only for
  reports received within `HUB_HEALTH_STALE_AGE` (15m, as in connection-log).
  An offline hub stops reporting, so its last state drops out of this series.
- `connectionlog_worker_hub_health_stale_state{hub_id}`: the stored state (0 to
  2) of the reports older than `HUB_HEALTH_STALE_AGE`. An unhealthy hub that goes
  offline keeps state 2 here, so a separate alert can still fire after the
  alert on `hub_health_state` has resolved.
- `connectionlog_worker_hub_health_reports{state}`: number of reports for `0`,
  `1`, `2`, `stale` and `inactive`. All five series are always present, so
  `absent()` on it detects a worker that is not scraped. Stale and inactive
  reports are not counted in `0`, `1`, `2`; an inactive hub counts as
  `inactive` whether its report is fresh or stale.

- `connectionlog_worker_hub_modules_outdated{hub_id}`: number of modules whose
  update has been pending for longer than `HUB_MODULE_OUTDATED_AGE`, only for hubs
  with at least one. Independent of `notified`.
- `connectionlog_worker_hub_health_missing{hub_id}`: 1 for hubs that are online
  in `gatewaystate` for longer than `HUB_HEALTH_STALE_AGE` and have no stored
  report, as with a device-cloud-connector before 0.8.0. Only exported if the
  health consumer is enabled (`HUB_HEALTH_TOPIC` neither empty nor `-`); otherwise
  no series is exported and `gatewaystate` is not read for it.

Inactive hubs have neither of the two series.

A failing Mongo query fails the whole scrape. The platform alert on unhealthy
hubs is defined in rancher-2-defs on these series; it is independent of the
owner notification above.

## Inactive hubs

A hub is inactive if its attribute `inactive` has the value `true` (case
insensitive), the same convention as for devices. device-repository sets the
attribute and publishes the full hub on the `hubs` topic; the worker stores one
document `{hub_id}` per inactive hub in `HUB_INACTIVE_COLLECTION` (unique index
on `hub_id`): a PUT with the attribute upserts it, a PUT without it and a DELETE
remove it. In batch mode the last command per hub counts.

An inactive hub raises no per-hub metric series (neither `hub_health_state` nor
`hub_health_stale_state`) and is only counted as `inactive`. The owner gets
neither the offline nor the unhealthy notification: the notification loops read
the hub from device-repository before sending, and for an inactive hub they
skip it and keep the tracking as notified, so the same episode is not notified
later either. Health reports of inactive hubs are still stored.

## Configuration

`HUB_HEALTH_TOPIC` defaults to `-`, which registers no consumer; prod enables it
together with the Kafka topic. `HUB_UNHEALTHY_NOTIFICATION_DELAY=-` disables the
notification and keeps the storage. `HUB_MODULE_OUTDATED_NOTIFICATION_INTERVAL`
(`1m`) set to `-` or empty disables the module notification and keeps tracking and
gauge; `HUB_MODULE_OUTDATED_AGE` applies to both. `PROMETHEUS_PORT` defaults to `-`, no
metrics server.
