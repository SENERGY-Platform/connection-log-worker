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
	"testing"

	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
)

type fakeHubController struct {
	Controller
	commands []model.HubCommand
}

func (this *fakeHubController) UpdateHub(command model.HubCommand) error {
	this.commands = append(this.commands, command)
	return nil
}

// the message format is the one device-repository publishes on the hubs topic
func TestHubsListenerPassesAttributes(t *testing.T) {
	control := &fakeHubController{}
	topic, listener, err := HubsListenerFactory(config.Config{HubTopic: "hubs"}, control)
	if err != nil || topic != "hubs" {
		t.Fatal(topic, err)
	}
	msg := `{"command":"PUT","id":"h1","owner":"o","hub":{"id":"h1","name":"n","attributes":[{"key":"inactive","value":"true","origin":"web-ui"}]}}`
	if err = listener([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	if len(control.commands) != 1 || control.commands[0].Id != "h1" || len(control.commands[0].Hub.Attributes) != 1 ||
		control.commands[0].Hub.Attributes[0].Key != "inactive" || control.commands[0].Hub.Attributes[0].Value != "true" {
		t.Errorf("unexpected commands %#v", control.commands)
	}
}
