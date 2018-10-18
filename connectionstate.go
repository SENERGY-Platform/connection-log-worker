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

import "gopkg.in/mgo.v2/bson"

func updateGatewayConnectorConnection(gatewayLog GatewayLog) (err error) {
	session, collection := getConnectorGatewayCollection()
	defer session.Close()
	_, err = collection.RemoveAll(ConnectorGatewayConnection{Gateway: gatewayLog.Gateway})
	if err != nil {
		return
	}
	if gatewayLog.Connected {
		err = collection.Insert(ConnectorGatewayConnection{Gateway: gatewayLog.Gateway, Connector: gatewayLog.Connector})
	}
	return
}

func updateDeviceConnectorConnection(deviceLog DeviceLog) (err error) {
	session, collection := getConnectorDeviceCollection()
	defer session.Close()
	_, err = collection.RemoveAll(ConnectorDeviceConnection{Device: deviceLog.Device})
	if err != nil {
		return
	}
	if deviceLog.Connected {
		err = collection.Insert(ConnectorDeviceConnection{Device: deviceLog.Device, Connector: deviceLog.Connector})
	}
	return
}

func disconnectMissingDevices(connectorLog ConnectorLog) (err error) {
	session, collection := getConnectorDeviceCollection()
	defer session.Close()
	result := []ConnectorDeviceConnection{}
	err = collection.Find(ConnectorDeviceConnection{Connector: connectorLog.Connector}).All(&result)
	if err != nil {
		return err
	}
	exists := map[string]bool{}
	for _, device := range connectorLog.Devices {
		exists[device.Device] = true
	}
	for _, connection := range result {
		if !exists[connection.Device] {
			err = sendDeviceLogEvent(DeviceLog{Connected: false, Device: connection.Device, Connector: connectorLog.Connector, Time: connectorLog.Time})
			if err != nil {
				return err
			}
		}
	}
	return
}

func disconnectMissingGateways(connectorLog ConnectorLog) (err error) {
	session, collection := getConnectorGatewayCollection()
	defer session.Close()
	result := []ConnectorGatewayConnection{}
	err = collection.Find(ConnectorGatewayConnection{Connector: connectorLog.Connector}).All(&result)
	if err != nil {
		return err
	}
	exists := map[string]bool{}
	for _, gateway := range connectorLog.Gateways {
		exists[gateway.Gateway] = true
	}
	for _, connection := range result {
		if !exists[connection.Gateway] {
			err = sendGatewayLogEvent(GatewayLog{Connected: false, Gateway: connection.Gateway, Connector: connectorLog.Connector, Time: connectorLog.Time})
			if err != nil {
				return err
			}
		}
	}
	return
}

func setGatewayState(gatewayLog GatewayLog) (update bool, err error) {
	session, collection := getGatewayStateCollection()
	defer session.Close()
	count, err := collection.Find(bson.M{"gateway": gatewayLog.Gateway, "online": gatewayLog.Connected}).Limit(1).Count()
	if err != nil {
		return false, err
	}
	update = count == 0
	if update {
		_, err = collection.Upsert(bson.M{"gateway": gatewayLog.Gateway}, GatewayState{Gateway: gatewayLog.Gateway, Online: gatewayLog.Connected})
	}
	return
}

func setDeviceState(deviceLog DeviceLog) (update bool, err error) {
	session, collection := getDeviceStateCollection()
	defer session.Close()
	count, err := collection.Find(bson.M{"device": deviceLog.Device, "online": deviceLog.Connected}).Limit(1).Count()
	if err != nil {
		return false, err
	}
	update = count == 0
	if update {
		_, err = collection.Upsert(bson.M{"device": deviceLog.Device}, DeviceState{Device: deviceLog.Device, Online: deviceLog.Connected})
	}
	return
}