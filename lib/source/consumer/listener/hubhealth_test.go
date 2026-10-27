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
	"errors"
	"testing"

	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
)

type fakeController struct {
	Controller
	healths []model.HubHealthMessage
	err     error
}

func (this *fakeController) LogHubHealth(message model.HubHealthMessage) error {
	this.healths = append(this.healths, message)
	return this.err
}

func TestHubHealthListener(t *testing.T) {
	valid := `{"hub_id":"h1","time":"2026-10-27T14:00:00Z","report":{"state":2,"reasons":["x"]}}`

	t.Run("disabled topic registers no listener", func(t *testing.T) {
		for _, topic := range []string{"", "-"} {
			_, listener, err := HubHealthListenerFactory(config.Config{HubHealthTopic: topic}, &fakeController{})
			if err != nil || listener != nil {
				t.Errorf("topic %q: expected no listener and no error, got %v %v", topic, listener != nil, err)
			}
		}
	})

	t.Run("valid message reaches the controller", func(t *testing.T) {
		control := &fakeController{}
		topic, listener, err := HubHealthListenerFactory(config.Config{HubHealthTopic: "gateway_health"}, control)
		if err != nil || listener == nil || topic != "gateway_health" {
			t.Fatal(topic, listener != nil, err)
		}
		if err = listener([]byte(valid)); err != nil {
			t.Fatal(err)
		}
		if len(control.healths) != 1 || control.healths[0].HubId != "h1" || control.healths[0].State != 2 {
			t.Errorf("unexpected controller calls %#v", control.healths)
		}
	})

	t.Run("malformed messages are skipped without an error", func(t *testing.T) {
		control := &fakeController{}
		_, listener, _ := HubHealthListenerFactory(config.Config{HubHealthTopic: "gateway_health"}, control)
		for _, raw := range []string{`not json`, `{}`, `{"hub_id":"h1","time":"x","report":{"state":1}}`, `{"hub_id":"h1","time":"2026-10-27T14:00:00Z","report":{"state":7}}`} {
			if err := listener([]byte(raw)); err != nil {
				t.Errorf("%s: expected the message to be skipped, got %v", raw, err)
			}
		}
		if len(control.healths) != 0 {
			t.Errorf("expected no controller calls, got %#v", control.healths)
		}
	})

	t.Run("controller errors are returned", func(t *testing.T) {
		control := &fakeController{err: errors.New("db down")}
		_, listener, _ := HubHealthListenerFactory(config.Config{HubHealthTopic: "gateway_health"}, control)
		if err := listener([]byte(valid)); err == nil {
			t.Error("expected the controller error")
		}
	})
}
