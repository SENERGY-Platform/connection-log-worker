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

package listener_batch

import (
	"encoding/json"

	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
)

func init() {
	Factories = append(Factories, DeviceLogListenerFactory)
}

func DeviceLogListenerFactory(config config.Config, control Controller) (topic string, listener Listener, err error) {
	return config.DeviceLogTopic, func(messages [][]byte) (err error) {
		var logs []model.DeviceLog
		for _, message := range messages {
			var log model.DeviceLog
			err = json.Unmarshal(message, &log)
			if err != nil {
				return
			}
			logs = append(logs, log)
		}
		return control.LogDevices(logs)
	}, nil
}
