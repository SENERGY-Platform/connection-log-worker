/*
 * Copyright 2026 InfAI (CC SES)
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
	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
)

func init() {
	Factories = append(Factories, HubHealthListenerFactory)
}

// HubHealthListenerFactory returns no listener while the topic is disabled. Reports come from gateways,
// so invalid ones are skipped and the valid ones of the batch are processed.
func HubHealthListenerFactory(config config.Config, control Controller) (topic string, listener Listener, err error) {
	if config.HubHealthTopic == "" || config.HubHealthTopic == "-" {
		return config.HubHealthTopic, nil, nil
	}
	return config.HubHealthTopic, func(messages [][]byte) (err error) {
		var valid []model.HubHealthMessage
		for _, raw := range messages {
			message, err := model.ParseHubHealthMessage(raw)
			if err != nil {
				config.GetLogger().Warn("skip invalid hub health message", "error", err)
				continue
			}
			valid = append(valid, message)
		}
		if len(valid) == 0 {
			return nil
		}
		return control.LogHubHealths(valid)
	}, nil
}
