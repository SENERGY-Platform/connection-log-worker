/*
 * Copyright 2018 InfAI (CC SES)
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

package main

import (
	"log"
	"sync"

	"time"

	"github.com/influxdata/influxdb/client/v2"
)

var influxdbInstance client.Client
var influxdbOnce sync.Once

func getInfluxDb() client.Client {
	influxdbOnce.Do(func() {
		var err error
		influxdbInstance, err = client.NewHTTPClient(client.HTTPConfig{
			Addr:     Config.InfluxdbUrl,
			Username: Config.InfluxdbUser,
			Password: Config.InfluxdbPw,
			Timeout:  time.Duration(Config.InfluxdbTimeout) * time.Second,
		})
		if err != nil {
			log.Fatal("unable to instantiate InfluxDB", err)
		}
	})
	return influxdbInstance
}

func logDeviceHistory(deviceLog DeviceLog) (err error) {
	bp, err := client.NewBatchPoints(client.BatchPointsConfig{
		Database:  Config.InfluxdbDb,
		Precision: "s",
	})
	if err != nil {
		return err
	}
	tags := map[string]string{
		"device":    deviceLog.Device,
		"connector": deviceLog.Connector,
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
	return getInfluxDb().Write(bp)
}

func logGatewayHistory(gatewayLog GatewayLog) error {
	bp, err := client.NewBatchPoints(client.BatchPointsConfig{
		Database:  Config.InfluxdbDb,
		Precision: "s",
	})
	if err != nil {
		return err
	}
	tags := map[string]string{
		"gateway":   gatewayLog.Gateway,
		"connector": gatewayLog.Connector,
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
	return getInfluxDb().Write(bp)
}

func logConnectorHistory(connectorLog ConnectorLog) error {
	bp, err := client.NewBatchPoints(client.BatchPointsConfig{
		Database:  Config.InfluxdbDb,
		Precision: "s",
	})
	if err != nil {
		return err
	}
	tags := map[string]string{
		"connector": connectorLog.Connector,
	}
	fields := map[string]interface{}{
		"connected": connectorLog.Connected,
	}
	pt, err := client.NewPoint(
		"connector",
		tags,
		fields,
		connectorLog.Time,
	)
	if err != nil {
		return err
	}
	bp.AddPoint(pt)
	return getInfluxDb().Write(bp)
}
