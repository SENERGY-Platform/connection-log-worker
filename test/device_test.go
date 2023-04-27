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

package test

import (
	"context"
	"encoding/json"
	"github.com/SENERGY-Platform/connection-log-worker/lib"
	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	"github.com/SENERGY-Platform/connection-log-worker/lib/source/consumer"
	"github.com/SENERGY-Platform/connection-log-worker/lib/source/util"
	"github.com/SENERGY-Platform/connection-log-worker/test/helper"
	"github.com/SENERGY-Platform/connection-log-worker/test/server"
	"github.com/google/uuid"
	"github.com/influxdata/influxdb/client/v2"
	"github.com/segmentio/kafka-go"
	"log"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestDevice(t *testing.T) {
	wg := &sync.WaitGroup{}
	defer wg.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defaultConfig, err := config.Load("../config.json")
	if err != nil {
		t.Error(err)
		return
	}
	defaultConfig.Debug = true

	config, connectionlogip, err := server.New(ctx, wg, defaultConfig)
	if err != nil {
		t.Error(err)
		return
	}
	connectionlog := "http://" + connectionlogip + ":8080"
	log.Println("DEBUG: connection-log-api-url:", connectionlog)

	err = lib.Start(ctx, config, func(err error, consumer *consumer.Consumer) {
		t.Error(err)
		return
	})
	if err != nil {
		t.Error(err)
		return
	}

	t.Run("check repeating state true", testStateUpdate(config, connectionlog, true, false))
	t.Run("check repeating state false", testStateUpdate(config, connectionlog, false, false))

	t.Run("check alternating state true", testStateUpdate(config, connectionlog, true, true))
	t.Run("check alternating state false", testStateUpdate(config, connectionlog, false, true))

	t.Run("check state after delete", testStateAfterDelete(config, connectionlog, true))
}

func testStateAfterDelete(config config.Config, connectionlog string, initialState bool) func(t *testing.T) {
	return func(t *testing.T) {
		var state bool = initialState
		var deviceId string
		var count = 1

		t.Run("create device", func(t *testing.T) {
			deviceId = createDevice(t, config.KafkaUrl)
		})
		time.Sleep(10 * time.Second)

		t.Run("send device log", func(t *testing.T) {
			sendLog(t, config.KafkaUrl, config.DeviceLogTopic, state, deviceId)
		})

		time.Sleep(10 * time.Second)

		t.Run("check device log", func(t *testing.T) {
			checkDeviceLog(t, connectionlog, deviceId, state, count)
		})

		t.Run("send device delete", func(t *testing.T) {
			sendDeviceDelete(t, config, deviceId)
		})

		time.Sleep(10 * time.Second)

		t.Run("check device log", func(t *testing.T) {
			checkDeviceLogs(t, connectionlog, []string{deviceId}, map[string]bool{}, 0)
		})
	}
}

func sendDeviceDelete(t *testing.T, config config.Config, id string) {
	b, err := json.Marshal(model.DeviceCommand{
		Command: "DELETE",
		Id:      id,
	})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := util.GetBroker(config.KafkaUrl)
	if err != nil {
		t.Fatal(err)
	}
	if len(broker) == 0 {
		t.Fatal(broker)
	}
	producer, err := helper.GetProducer(broker, config.DeviceTopic, true)
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	defer time.Sleep(2 * time.Second)
	err = producer.WriteMessages(
		context.Background(),
		kafka.Message{
			Key:   []byte(id),
			Value: b,
			Time:  time.Now(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
}

func testStateUpdate(config config.Config, connectionlog string, initialState bool, alternating bool) func(t *testing.T) {
	return func(t *testing.T) {
		var state bool = initialState
		var deviceId string
		var hubId string
		var count = 1

		t.Run("create device", func(t *testing.T) {
			deviceId = createDevice(t, config.KafkaUrl)
		})

		t.Run("create hub", func(t *testing.T) {
			hubId = createHub(t, config.KafkaUrl)
		})

		time.Sleep(10 * time.Second)

		t.Run("send device log", func(t *testing.T) {
			sendLog(t, config.KafkaUrl, config.DeviceLogTopic, state, deviceId)
		})

		t.Run("send hub log", func(t *testing.T) {
			sendLog(t, config.KafkaUrl, config.HubLogTopic, state, hubId)
		})

		if alternating {
			state = !state
			count = 2
		}

		t.Run("send device log 2", func(t *testing.T) {
			sendLog(t, config.KafkaUrl, config.DeviceLogTopic, state, deviceId)
		})

		t.Run("send hub log 2", func(t *testing.T) {
			sendLog(t, config.KafkaUrl, config.HubLogTopic, state, hubId)
		})

		time.Sleep(10 * time.Second)

		t.Run("check device log", func(t *testing.T) {
			checkDeviceLog(t, connectionlog, deviceId, state, count)
		})

		t.Run("check hub log", func(t *testing.T) {
			checkHubLog(t, connectionlog, hubId, state, count)
		})
	}
}

func checkDeviceLog(t *testing.T, connectionlogUrl string, id string, state bool, count int) {
	t.Run("check device state", func(t *testing.T) {
		checkDeviceState(t, connectionlogUrl, id, state)
	})
	t.Run("check device history", func(t *testing.T) {
		checkDeviceHistory(t, connectionlogUrl, id, count)
	})
}

func checkDeviceLogs(t *testing.T, connectionlogUrl string, ids []string, expectedStates map[string]bool, count int) {
	t.Run("check device state", func(t *testing.T) {
		checkDeviceStates(t, connectionlogUrl, ids, expectedStates)
	})
	t.Run("check device history", func(t *testing.T) {
		checkDeviceHistorys(t, connectionlogUrl, ids, count)
	})
}

func checkDeviceHistory(t *testing.T, connectionlogUrl string, id string, count int) {
	checkDeviceHistorys(t, connectionlogUrl, []string{id}, count)
}

func checkDeviceHistorys(t *testing.T, connectionlogUrl string, ids []string, count int) {
	result := []client.Result{}
	err := helper.AdminPost(connectionlogUrl+"/intern/history/device/1h", ids, &result)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatal(len(result), result)
	}
	if result[0].Err != "" {
		t.Fatal(result[0].Err)
	}
	if len(result[0].Series) != 1 {
		if count != 0 {
			t.Fatal(len(result[0].Series), result[0].Series)
		}
		return
	}
	if len(result[0].Series[0].Values) != count {
		t.Fatal(len(result[0].Series[0].Values), result[0].Series[0].Values)
	}
}

func checkDeviceState(t *testing.T, connectionlogUrl string, id string, state bool) {
	checkDeviceStates(t, connectionlogUrl, []string{id}, map[string]bool{id: state})
}

func checkDeviceStates(t *testing.T, connectionlogUrl string, ids []string, expected map[string]bool) {
	result := map[string]bool{}
	err := helper.AdminPost(connectionlogUrl+"/intern/state/device/check", ids, &result)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expected, result) {
		t.Error(result, "\n", expected)
	}
}

func checkHubLog(t *testing.T, connectionlogUrl string, id string, state bool, count int) {
	t.Run("check hub state", func(t *testing.T) {
		checkHubState(t, connectionlogUrl, id, state)
	})
	t.Run("check hub history", func(t *testing.T) {
		checkHubHistory(t, connectionlogUrl, id, count)
	})
}

func checkHubHistory(t *testing.T, connectionlogUrl string, id string, count int) {
	result := []client.Result{}
	err := helper.AdminPost(connectionlogUrl+"/intern/history/gateway/1h", []string{id}, &result)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatal(len(result), result)
	}
	if result[0].Err != "" {
		t.Fatal(result[0].Err)
	}
	if len(result[0].Series) != 1 {
		t.Fatal(len(result[0].Series), result[0].Series)
	}
	if len(result[0].Series[0].Values) != count {
		t.Fatal(len(result[0].Series[0].Values), result[0].Series[0].Values)
	}
}

func checkHubState(t *testing.T, connectionlogUrl string, id string, state bool) {
	result := map[string]bool{}
	err := helper.AdminPost(connectionlogUrl+"/intern/state/gateway/check", []string{id}, &result)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[id] != state {
		t.Fatal(result)
	}
}

func sendLog(t *testing.T, kafkaUrl string, topic string, state bool, id string) {
	b, err := json.Marshal(model.DeviceLog{
		Id:        id,
		Connected: state,
		Time:      time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := util.GetBroker(kafkaUrl)
	if err != nil {
		t.Fatal(err)
	}
	if len(broker) == 0 {
		t.Fatal(broker)
	}
	producer, err := helper.GetProducer(broker, topic, true)
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	defer time.Sleep(2 * time.Second)
	err = producer.WriteMessages(
		context.Background(),
		kafka.Message{
			Key:   []byte("cmd.Id"),
			Value: b,
			Time:  time.Now(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
}

func createDevice(t *testing.T, kafkaUrl string) (id string) {
	id = uuid.NewString()
	broker, err := util.GetBroker(kafkaUrl)
	if err != nil {
		t.Fatal(err)
	}
	if len(broker) == 0 {
		t.Fatal(broker)
	}
	producer, err := helper.GetProducer(broker, "devices", true)
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	defer time.Sleep(2 * time.Second)
	err = producer.WriteMessages(
		context.Background(),
		kafka.Message{
			Key:   []byte("cmd.Id"),
			Value: []byte(`{"command":"PUT","id":"` + id + `","owner":"dd69ea0d-f553-4336-80f3-7f4567f85c7b","device":{"id":"` + id + `","local_id":"device-local-id","name":"device-name","device_type_id":"dt-id"}}`),
			Time:  time.Now(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func createHub(t *testing.T, kafkaUrl string) (id string) {
	id = uuid.NewString()
	broker, err := util.GetBroker(kafkaUrl)
	if err != nil {
		t.Fatal(err)
	}
	if len(broker) == 0 {
		t.Fatal(broker)
	}
	producer, err := helper.GetProducer(broker, "hubs", true)
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	defer time.Sleep(2 * time.Second)
	err = producer.WriteMessages(
		context.Background(),
		kafka.Message{
			Key:   []byte("cmd.Id"),
			Value: []byte(`{"command":"PUT","id":"` + id + `","owner":"dd69ea0d-f553-4336-80f3-7f4567f85c7b","hub":{"id":"` + id + `","name":"hub-name","hash":"hash-value","device_local_ids":["device-local-id"]}}`),
			Time:  time.Now(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	return id
}
