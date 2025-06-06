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

package main

import (
	"flag"
	"fmt"
	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	"log"
	"os"

	"github.com/swaggest/go-asyncapi/reflector/asyncapi-2.4.0"
	"github.com/swaggest/go-asyncapi/spec-2.4.0"
)

//go:generate go run main.go

func main() {
	configLocation := flag.String("config", "../../config.json", "configuration file")
	flag.Parse()

	conf, err := config.Load(*configLocation)
	if err != nil {
		log.Fatal("ERROR: unable to load config", err)
	}

	asyncAPI := spec.AsyncAPI{}
	asyncAPI.Info.Title = "Connection Log Worker"

	asyncAPI.AddServer("kafka", spec.Server{
		URL:      conf.KafkaUrl,
		Protocol: "kafka",
	})

	reflector := asyncapi.Reflector{}
	reflector.Schema = &asyncAPI

	mustNotFail := func(err error) {
		if err != nil {
			panic(err.Error())
		}
	}

	mustNotFail(reflector.AddChannel(asyncapi.ChannelInfo{
		Name: conf.HubLogTopic,
		Publish: &asyncapi.MessageSample{
			MessageEntity: spec.MessageEntity{
				Name:  "HubLog",
				Title: "HubLog",
			},
			MessageSample: new(model.HubLog),
		},
	}))

	mustNotFail(reflector.AddChannel(asyncapi.ChannelInfo{
		Name: conf.DeviceLogTopic,
		Publish: &asyncapi.MessageSample{
			MessageEntity: spec.MessageEntity{
				Name:  "DeviceLog",
				Title: "DeviceLog",
			},
			MessageSample: new(model.DeviceLog),
		},
	}))

	mustNotFail(reflector.AddChannel(asyncapi.ChannelInfo{
		Name: conf.DeviceTopic,
		Publish: &asyncapi.MessageSample{
			MessageEntity: spec.MessageEntity{
				Name:  "DeviceCommand",
				Title: "DeviceCommand",
			},
			MessageSample: new(model.DeviceCommand),
		},
	}))

	mustNotFail(reflector.AddChannel(asyncapi.ChannelInfo{
		Name: conf.HubTopic,
		Publish: &asyncapi.MessageSample{
			MessageEntity: spec.MessageEntity{
				Name:  "HubCommand",
				Title: "HubCommand",
			},
			MessageSample: new(model.HubCommand),
		},
	}))

	buff, err := reflector.Schema.MarshalJSON()
	mustNotFail(err)

	fmt.Println(string(buff))
	mustNotFail(os.WriteFile("asyncapi.json", buff, 0o600))
}
