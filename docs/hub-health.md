# Hub health

The worker stores the latest health report per hub from Kafka `gateway_health`
and notifies the owner when a hub stays unhealthy. connection-log serves the
stored reports. The message format is defined by senergy-platform-connector
(`docs/health-reports.md` there), the report itself by
mgw-device-cloud-connector.

## Applies when

You change the health consumer, the `gatewayhealth` document, or the unhealthy
notification. connection-log reads `gatewayhealth` with a read-only Mongo user,
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
only the newest message per hub is written.

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
unhealthy. Deleting a hub removes both documents.

## Configuration

`HUB_HEALTH_TOPIC` defaults to `-`, which registers no consumer; prod enables it
together with the Kafka topic. `HUB_UNHEALTHY_NOTIFICATION_DELAY=-` disables the
notification and keeps the storage.
