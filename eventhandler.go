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
	"encoding/json"

	"time"

	"log"

	"github.com/SmartEnergyPlatform/amqp-wrapper-lib"
)

type ConnectorLog struct {
	Connected bool         `json:"connected"`
	Connector string       `json:"connector"`
	Gateways  []GatewayLog `json:"gateways"`
	Devices   []DeviceLog  `json:"devices"`
	Time      time.Time    `json:"time"`
}

type GatewayLog struct {
	Connected bool      `json:"connected"`
	Connector string    `json:"connector"`
	Gateway   string    `json:"gateway"`
	Time      time.Time `json:"time"`
}

type DeviceLog struct {
	Connected bool      `json:"connected"`
	Connector string    `json:"connector"`
	Device    string    `json:"device"`
	Time      time.Time `json:"time"`
}

func getDeviceLogHandler() amqp_wrapper_lib.ConsumerFunc {
	return func(delivery []byte) (err error) {
		devicelog := DeviceLog{}
		err = json.Unmarshal(delivery, &devicelog)
		if err != nil {
			return
		}
		log.Println("DEBUG: handle device log update", devicelog)
		err = updateDeviceConnectorConnection(devicelog)
		if err != nil {
			return
		}
		updated, err := setDeviceState(devicelog)
		if err != nil {
			return err
		}
		if updated {
			err = logDeviceHistory(devicelog)
		}
		return
	}
}

func getGatewayLogHandler() amqp_wrapper_lib.ConsumerFunc {
	return func(delivery []byte) (err error) {
		gatewayLog := GatewayLog{}
		err = json.Unmarshal(delivery, &gatewayLog)
		if err != nil {
			return
		}
		log.Println("DEBUG: handle gw log update", gatewayLog)
		err = updateGatewayConnectorConnection(gatewayLog)
		if err != nil {
			return
		}
		updated, err :=  setGatewayState(gatewayLog)
		if err != nil {
			return err
		}
		if updated {
			err = logGatewayHistory(gatewayLog)
		}
		return
	}
}

func getConnectorLogHandler() amqp_wrapper_lib.ConsumerFunc {
	return func(delivery []byte) (err error) {
		connectorlog := ConnectorLog{}
		err = json.Unmarshal(delivery, &connectorlog)
		if err != nil {
			return
		}
		log.Println("DEBUG: handle connector log update", connectorlog)
		err = disconnectMissingGateways(connectorlog)
		log.Println("DEBUG: finished disconnectMissingGateways", err)
		if err != nil {
			return
		}
		err = disconnectMissingDevices(connectorlog)
		log.Println("DEBUG: finished disconnectMissingDevices", err)
		if err != nil {
			return
		}
		err = sendGatewayLogEvents(connectorlog)
		log.Println("DEBUG: finished sendGatewayLogEvents", err)
		if err != nil {
			return
		}
		err = sendDeviceLogEvents(connectorlog)
		log.Println("DEBUG: finished sendDeviceLogEvents", err)
		if err != nil {
			return
		}
		err = logConnectorHistory(connectorlog)
		log.Println("DEBUG: finished logConnectorHistory", err)
		return
	}
}
