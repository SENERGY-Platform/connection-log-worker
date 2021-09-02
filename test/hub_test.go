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
	"github.com/influxdata/influxdb/client/v2"
	"github.com/segmentio/kafka-go"
	"log"
	"reflect"
	"testing"
	"time"
)

func TestHub(t *testing.T) {
	defaultConfig, err := config.Load("../config.json")
	if err != nil {
		t.Error(err)
		return
	}
	defaultConfig.Debug = true

	ctx, cancel := context.WithCancel(context.Background())
	defer time.Sleep(10 * time.Second) //wait for docker cleanup
	defer cancel()

	config, connectionlogip, err := server.New(ctx, defaultConfig)
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

	t.Run("check state after delete", testHubStateAfterDelete(config, connectionlog, true))
}

func testHubStateAfterDelete(config config.Config, connectionlog string, initialState bool) func(t *testing.T) {
	return func(t *testing.T) {
		var state bool = initialState
		var hubId string
		var count = 1

		t.Run("create hub", func(t *testing.T) {
			hubId = createHub(t, config.KafkaUrl)
		})
		time.Sleep(10 * time.Second)

		t.Run("send hub log", func(t *testing.T) {
			sendLog(t, config.KafkaUrl, config.HubLogTopic, state, hubId)
		})

		time.Sleep(10 * time.Second)

		t.Run("check hub log", func(t *testing.T) {
			checkHubLog(t, connectionlog, hubId, state, count)
		})

		t.Run("send hub delete", func(t *testing.T) {
			sendHubDelete(t, config, hubId)
		})

		time.Sleep(10 * time.Second)

		t.Run("check hub log", func(t *testing.T) {
			checkHubLogs(t, connectionlog, []string{hubId}, map[string]bool{}, 0)
		})
	}
}

func sendHubDelete(t *testing.T, config config.Config, id string) {
	b, err := json.Marshal(model.HubCommand{
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
	producer, err := helper.GetProducer(broker, config.HubTopic, true)
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

func checkHubLogs(t *testing.T, connectionlogUrl string, ids []string, expectedStates map[string]bool, count int) {
	t.Run("check hub state", func(t *testing.T) {
		checkHubStates(t, connectionlogUrl, ids, expectedStates)
	})
	t.Run("check hub history", func(t *testing.T) {
		checkHubHistories(t, connectionlogUrl, ids, count)
	})
}

func checkHubHistories(t *testing.T, connectionlogUrl string, ids []string, count int) {
	result := []client.Result{}
	err := helper.AdminPost(connectionlogUrl+"/intern/history/gateway/1h", ids, &result)
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

func checkHubStates(t *testing.T, connectionlogUrl string, ids []string, expected map[string]bool) {
	result := map[string]bool{}
	err := helper.AdminPost(connectionlogUrl+"/intern/state/gateway/check", ids, &result)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expected, result) {
		t.Error(result, "\n", expected)
	}
}
