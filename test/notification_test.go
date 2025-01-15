/*
 * Copyright 2025 InfAI (CC SES)
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
	"github.com/SENERGY-Platform/connection-log-worker/lib"
	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	"github.com/SENERGY-Platform/connection-log-worker/lib/source/consumer"
	"github.com/SENERGY-Platform/connection-log-worker/lib/source/util"
	"github.com/SENERGY-Platform/connection-log-worker/test/helper"
	"github.com/SENERGY-Platform/connection-log-worker/test/server"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNotifications(t *testing.T) {
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
	defaultConfig.RoundTime = "1s"

	conf, err := server.NewPartial(ctx, wg, defaultConfig)
	if err != nil {
		t.Error(err)
		return
	}

	mux := sync.Mutex{}
	notifications := []string{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.Lock()
		defer mux.Unlock()
		temp, _ := io.ReadAll(r.Body)
		notifications = append(notifications, strings.TrimSpace(string(temp)))
	}))
	defer s.Close()
	conf.NotificationUrl = s.URL

	err = lib.Start(ctx, conf, func(err error, consumer *consumer.Consumer) {
		t.Error(err)
		return
	})
	if err != nil {
		t.Error(err)
		return
	}

	broker, err := util.GetBroker(conf.KafkaUrl)
	if err != nil {
		t.Fatal(err)
	}
	if len(broker) == 0 {
		t.Fatal(broker)
	}
	producer, err := helper.GetProducer(broker, conf.DeviceLogTopic, true)
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()

	sendFullDeviceLog(t, producer, model.DeviceLog{
		Id:                     "id1",
		Connected:              false,
		Time:                   time.Now(),
		MonitorConnectionState: "2s",
		DeviceOwner:            "testowner",
		DeviceName:             "device 1 msg1",
	})
	sendFullDeviceLog(t, producer, model.DeviceLog{
		Id:                     "id2",
		Connected:              false,
		Time:                   time.Now(),
		MonitorConnectionState: "2s",
		DeviceOwner:            "testowner",
		DeviceName:             "device 2 msg1",
	})
	sendFullDeviceLog(t, producer, model.DeviceLog{
		Id:          "id3",
		Connected:   false,
		Time:        time.Now(),
		DeviceOwner: "testowner",
		DeviceName:  "device 3",
	})

	time.Sleep(1 * time.Second)

	sendFullDeviceLog(t, producer, model.DeviceLog{
		Id:                     "id1",
		Connected:              true,
		Time:                   time.Now(),
		MonitorConnectionState: "2s",
		DeviceOwner:            "testowner",
		DeviceName:             "device 1 msg2",
	})
	sendFullDeviceLog(t, producer, model.DeviceLog{
		Id:                     "id2",
		Connected:              false,
		Time:                   time.Now(),
		MonitorConnectionState: "2s",
		DeviceOwner:            "testowner",
		DeviceName:             "device 2 msg2",
	})

	time.Sleep(1500 * time.Millisecond)

	sendFullDeviceLog(t, producer, model.DeviceLog{
		Id:                     "id1",
		Connected:              false,
		Time:                   time.Now(),
		MonitorConnectionState: "2s",
		DeviceOwner:            "testowner",
		DeviceName:             "device 1 msg3",
	})
	sendFullDeviceLog(t, producer, model.DeviceLog{
		Id:                     "id2",
		Connected:              false,
		Time:                   time.Now(),
		MonitorConnectionState: "2s",
		DeviceOwner:            "testowner",
		DeviceName:             "device 2 msg3",
	})

	time.Sleep(1 * time.Second)

	sendFullDeviceLog(t, producer, model.DeviceLog{
		Id:                     "id1",
		Connected:              true,
		Time:                   time.Now(),
		MonitorConnectionState: "2s",
		DeviceOwner:            "testowner",
		DeviceName:             "device 1 msg4",
	})
	sendFullDeviceLog(t, producer, model.DeviceLog{
		Id:                     "id2",
		Connected:              false,
		Time:                   time.Now(),
		MonitorConnectionState: "2s",
		DeviceOwner:            "testowner",
		DeviceName:             "device 2 msg4",
	})

	time.Sleep(1 * time.Second)

	sendFullDeviceLog(t, producer, model.DeviceLog{
		Id:                     "id1",
		Connected:              true,
		Time:                   time.Now(),
		MonitorConnectionState: "2s",
		DeviceOwner:            "testowner",
		DeviceName:             "device 1 msg5",
	})
	sendFullDeviceLog(t, producer, model.DeviceLog{
		Id:                     "id2",
		Connected:              false,
		Time:                   time.Now(),
		MonitorConnectionState: "2s",
		DeviceOwner:            "testowner",
		DeviceName:             "device 2 msg5",
	})

	time.Sleep(1 * time.Second)

	sendFullDeviceLog(t, producer, model.DeviceLog{
		Id:                     "id1",
		Connected:              true,
		Time:                   time.Now(),
		MonitorConnectionState: "2s",
		DeviceOwner:            "testowner",
		DeviceName:             "device 1 msg6",
	})
	sendFullDeviceLog(t, producer, model.DeviceLog{
		Id:                     "id2",
		Connected:              true,
		Time:                   time.Now(),
		MonitorConnectionState: "2s",
		DeviceOwner:            "testowner",
		DeviceName:             "device 2 msg6",
	})

	time.Sleep(1 * time.Second)

	sendFullDeviceLog(t, producer, model.DeviceLog{
		Id:                     "id1",
		Connected:              true,
		Time:                   time.Now(),
		MonitorConnectionState: "2s",
		DeviceOwner:            "testowner",
		DeviceName:             "device 1 msg7",
	})
	sendFullDeviceLog(t, producer, model.DeviceLog{
		Id:                     "id2",
		Connected:              false,
		Time:                   time.Now(),
		MonitorConnectionState: "2s",
		DeviceOwner:            "testowner",
		DeviceName:             "device 2 msg7",
	})

	time.Sleep(2500 * time.Millisecond)

	sendFullDeviceLog(t, producer, model.DeviceLog{
		Id:                     "id1",
		Connected:              true,
		Time:                   time.Now(),
		MonitorConnectionState: "2s",
		DeviceOwner:            "testowner",
		DeviceName:             "device 1 msg7",
	})
	sendFullDeviceLog(t, producer, model.DeviceLog{
		Id:                     "id2",
		Connected:              false,
		Time:                   time.Now(),
		MonitorConnectionState: "2s",
		DeviceOwner:            "testowner",
		DeviceName:             "device 2 msg7",
	})
	sendFullDeviceLog(t, producer, model.DeviceLog{
		Id:          "id3",
		Connected:   false,
		Time:        time.Now(),
		DeviceOwner: "testowner",
		DeviceName:  "device 3",
	})

	time.Sleep(1 * time.Second)

	mux.Lock()
	defer mux.Unlock()
	t.Logf("%#v\n", notifications)

	expected := []string{
		"{\"userId\":\"testowner\",\"title\":\"Device Offline\",\"message\":\"device device 2 msg3 (id2) has been offline for 3s\",\"topic\":\"device_offline\"}",
		"{\"userId\":\"testowner\",\"title\":\"Device Offline\",\"message\":\"device device 2 msg7 (id2) has been offline for 3s\",\"topic\":\"device_offline\"}",
	}

	if !reflect.DeepEqual(notifications, expected) {
		t.Errorf("\ne:%v\na:%v\n", expected, notifications)
	}
}
