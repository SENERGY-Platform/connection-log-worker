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
	"log"

	"github.com/SmartEnergyPlatform/amqp-wrapper-lib"
)

var conn *amqp_wrapper_lib.Connection

func InitEventHandling() {
	var err error
	conn, err = amqp_wrapper_lib.Init(Config.AmqpUrl, []string{Config.ConnectorLogTopic, Config.DeviceLogTopic, Config.GatewayLogTopic}, Config.AmqpReconnectTimeout)
	if err != nil {
		log.Fatal("ERROR: while initializing amqp connection", err)
		return
	}
	conn.SetMessageLogging(Config.AmqpMsgLogging == "true")
	log.Println("init ConnectorLog event handler")
	err = conn.Consume(Config.AmqpConsumerName+"_"+Config.ConnectorLogTopic, Config.ConnectorLogTopic, getConnectorLogHandler())
	if err != nil {
		log.Fatal("ERROR: while initializing ConnectorLog consumer", err)
		return
	}

	log.Println("init DeviceLog event handler")
	err = conn.Consume(Config.AmqpConsumerName+"_"+Config.DeviceLogTopic, Config.DeviceLogTopic, getDeviceLogHandler())
	if err != nil {
		log.Fatal("ERROR: while initializing DeviceLog consumer", err)
		return
	}

	log.Println("init GatewayLog event handler")
	err = conn.Consume(Config.AmqpConsumerName+"_"+Config.GatewayLogTopic, Config.GatewayLogTopic, getGatewayLogHandler())
	if err != nil {
		log.Fatal("ERROR: while initializing GatewayLog consumer", err)
		return
	}
	return
}

func sendEvent(topic string, event interface{}) error {
	payload, err := json.Marshal(event)
	if err != nil {
		log.Println("ERROR: event marshaling:", err)
		return err
	}
	if Config.AmqpMsgLogging == "true" {
		log.Println("DEBUG: send amqp event: ", topic, string(payload))
	}
	err = conn.Publish(topic, payload)
	if err != nil {
		log.Println("ERROR while sending", err)
	}
	return err
}

func sendDeviceLogEvents(connectorLog ConnectorLog) (err error) {
	for _, deviceLog := range connectorLog.Devices {
		deviceLog.Time = connectorLog.Time
		deviceLog.Connector = connectorLog.Connector
		err = sendDeviceLogEvent(deviceLog)
		if err != nil {
			return err
		}
	}
	return
}

func sendDeviceLogEvent(deviceLog DeviceLog) error {
	return sendEvent(Config.DeviceLogTopic, deviceLog)
}

func sendGatewayLogEvents(connectorLog ConnectorLog) (err error) {
	for _, gatewayLog := range connectorLog.Gateways {
		gatewayLog.Time = connectorLog.Time
		gatewayLog.Connector = connectorLog.Connector
		err = sendGatewayLogEvent(gatewayLog)
		if err != nil {
			return err
		}
	}
	return
}

func sendGatewayLogEvent(gatewayLog GatewayLog) error {
	return sendEvent(Config.GatewayLogTopic, gatewayLog)
}
