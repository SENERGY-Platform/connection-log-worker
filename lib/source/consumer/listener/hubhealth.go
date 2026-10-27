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

package listener

import (
	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
)

func init() {
	Factories = append(Factories, HubHealthListenerFactory)
}

// HubHealthListenerFactory returns no listener while the topic is disabled. Reports come from gateways,
// so an invalid one is skipped instead of returned: an error here would end in the consumer's fatal retry path.
func HubHealthListenerFactory(config config.Config, control Controller) (topic string, listener Listener, err error) {
	if config.HubHealthTopic == "" || config.HubHealthTopic == "-" {
		return config.HubHealthTopic, nil, nil
	}
	return config.HubHealthTopic, func(msg []byte) (err error) {
		message, err := model.ParseHubHealthMessage(msg)
		if err != nil {
			config.GetLogger().Warn("skip invalid hub health message", "error", err)
			return nil
		}
		return control.LogHubHealth(message)
	}, nil
}
