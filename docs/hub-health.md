# Hub health

The worker stores the latest health report per hub from Kafka `gateway_health`
and notifies the owner when a hub stays unhealthy. connection-log serves the
stored reports. The message format is defined by senergy-platform-connector
(`docs/health-reports.md` there), the report itself by
mgw-device-cloud-connector.

## Applies when

You change the health consumer, the `gatewayhealth` document, the unhealthy
notification, the metrics, or the handling of inactive hubs. connection-log reads `gatewayhealth` with a read-only Mongo user,
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

## Metrics

With `PROMETHEUS_PORT` set, `/metrics` serves three gauges, read from
`gatewayhealth` and `HUB_INACTIVE_COLLECTION` on every scrape:

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
notification and keeps the storage. `PROMETHEUS_PORT` defaults to `-`, no
metrics server.
