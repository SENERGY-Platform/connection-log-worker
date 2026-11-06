/*
 * Copyright 2019 InfAI (CC SES)
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
)

// historyTimeout bounds a single history statement.
const historyTimeout = 30 * time.Second

func (this *Controller) logDeviceHistory(deviceLog model.DeviceLog) (err error) {
	return this.writeDeviceLogs([]model.DeviceLog{deviceLog})
}

func (this *Controller) logGatewayHistory(gatewayLog model.HubLog) error {
	return this.writeHubLogs([]model.HubLog{gatewayLog})
}

func (this *Controller) writeDeviceLogs(deviceLogs []model.DeviceLog) (err error) {
	ids := make([]string, len(deviceLogs))
	times := make([]time.Time, len(deviceLogs))
	connected := make([]bool, len(deviceLogs))
	for i, deviceLog := range deviceLogs {
		ids[i], times[i], connected[i] = deviceLog.Id, deviceLog.Time, deviceLog.Connected
	}
	return this.insertHistory(deviceTable, ids, times, connected)
}

func (this *Controller) writeHubLogs(gatewayLogs []model.HubLog) (err error) {
	ids := make([]string, len(gatewayLogs))
	times := make([]time.Time, len(gatewayLogs))
	connected := make([]bool, len(gatewayLogs))
	for i, gatewayLog := range gatewayLogs {
		ids[i], times[i], connected[i] = gatewayLog.Id, gatewayLog.Time, gatewayLog.Connected
	}
	return this.insertHistory(gatewayTable, ids, times, connected)
}

// insertHistory writes all rows in one statement. Kafka delivers at least once, so a row that exists already
// (same id and time) is skipped. Two states of one id with exactly the same time therefore keep the first one,
// where InfluxDB kept the last; time has microsecond resolution, so only a redelivery hits this in practice.
// table is always one of the table constants.
func (this *Controller) insertHistory(table string, ids []string, times []time.Time, connected []bool) error {
	if len(ids) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), historyTimeout)
	defer cancel()
	_, err := this.db.Exec(ctx,
		fmt.Sprintf(`INSERT INTO %s (id, time, connected)
SELECT * FROM unnest($1::text[], $2::timestamptz[], $3::boolean[])
ON CONFLICT (id, time) DO NOTHING`, table),
		ids, times, connected)
	return err
}

func (this *Controller) deleteDeviceLog(deviceId string) (err error) {
	return this.deleteHistory(deviceTable, []string{deviceId})
}

func (this *Controller) deleteGatewayLog(gwId string) (err error) {
	return this.deleteHistory(gatewayTable, []string{gwId})
}

func (this *Controller) deleteDeviceLogs(deviceIds []string) (err error) {
	return this.deleteHistory(deviceTable, deviceIds)
}

func (this *Controller) deleteGatewayLogs(gwIds []string) (err error) {
	return this.deleteHistory(gatewayTable, gwIds)
}

// deleteHistory removes all rows of the ids. table is always one of the table constants.
func (this *Controller) deleteHistory(table string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), historyTimeout)
	defer cancel()
	_, err := this.db.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id = ANY($1)`, table), ids)
	return err
}
