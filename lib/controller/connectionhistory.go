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
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	"log"

	"time"

	"github.com/influxdata/influxdb/client/v2"
)

func (this *Controller) getInfluxDb() client.Client {
	this.influxdbOnce.Do(func() {
		var err error
		this.influxdbInstance, err = client.NewHTTPClient(client.HTTPConfig{
			Addr:     this.config.InfluxdbUrl,
			Username: this.config.InfluxdbUser,
			Password: this.config.InfluxdbPw,
			Timeout:  time.Duration(this.config.InfluxdbTimeout) * time.Second,
		})
		if err != nil {
			log.Fatal("unable to instantiate InfluxDB", err)
		}
	})
	return this.influxdbInstance
}

func (this *Controller) logDeviceHistory(deviceLog model.DeviceLog) (err error) {
	bp, err := client.NewBatchPoints(client.BatchPointsConfig{
		Database:  this.config.InfluxdbDb,
		Precision: "s",
	})
	if err != nil {
		return err
	}
	tags := map[string]string{
		"device": deviceLog.Id,
	}
	fields := map[string]interface{}{
		"connected": deviceLog.Connected,
	}
	pt, err := client.NewPoint(
		"device",
		tags,
		fields,
		deviceLog.Time,
	)
	if err != nil {
		return err
	}
	bp.AddPoint(pt)
	return this.getInfluxDb().Write(bp)
}

func (this *Controller) logGatewayHistory(gatewayLog model.HubLog) error {
	bp, err := client.NewBatchPoints(client.BatchPointsConfig{
		Database:  this.config.InfluxdbDb,
		Precision: "s",
	})
	if err != nil {
		return err
	}
	tags := map[string]string{
		"gateway": gatewayLog.Id,
	}
	fields := map[string]interface{}{
		"connected": gatewayLog.Connected,
	}
	pt, err := client.NewPoint(
		"gateway",
		tags,
		fields,
		gatewayLog.Time,
	)
	if err != nil {
		return err
	}
	bp.AddPoint(pt)
	return this.getInfluxDb().Write(bp)
}
