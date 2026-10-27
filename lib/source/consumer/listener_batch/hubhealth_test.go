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
	"errors"
	"testing"

	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
)

type fakeController struct {
	Controller
	calls [][]model.HubHealthMessage
	err   error
}

func (this *fakeController) LogHubHealths(messages []model.HubHealthMessage) error {
	this.calls = append(this.calls, messages)
	return this.err
}

func TestHubHealthListener(t *testing.T) {
	valid := func(hub string) []byte {
		return []byte(`{"hub_id":"` + hub + `","time":"2026-10-27T14:00:00Z","report":{"state":2}}`)
	}

	t.Run("disabled topic registers no listener", func(t *testing.T) {
		for _, topic := range []string{"", "-"} {
			_, listener, err := HubHealthListenerFactory(config.Config{HubHealthTopic: topic}, &fakeController{})
			if err != nil || listener != nil {
				t.Errorf("topic %q: expected no listener and no error, got %v %v", topic, listener != nil, err)
			}
		}
	})

	t.Run("valid messages of a batch with malformed ones are processed", func(t *testing.T) {
		control := &fakeController{}
		topic, listener, err := HubHealthListenerFactory(config.Config{HubHealthTopic: "gateway_health"}, control)
		if err != nil || listener == nil || topic != "gateway_health" {
			t.Fatal(topic, listener != nil, err)
		}
		err = listener([][]byte{valid("h1"), []byte(`not json`), valid("h2"), []byte(`{"hub_id":"h3"}`)})
		if err != nil {
			t.Fatal(err)
		}
		if len(control.calls) != 1 || len(control.calls[0]) != 2 || control.calls[0][0].HubId != "h1" || control.calls[0][1].HubId != "h2" {
			t.Errorf("unexpected controller calls %#v", control.calls)
		}
	})

	t.Run("a batch without valid messages is not an error", func(t *testing.T) {
		control := &fakeController{}
		_, listener, _ := HubHealthListenerFactory(config.Config{HubHealthTopic: "gateway_health"}, control)
		if err := listener([][]byte{[]byte(`not json`)}); err != nil {
			t.Errorf("expected the batch to be skipped, got %v", err)
		}
		if len(control.calls) != 0 {
			t.Errorf("expected no controller calls, got %#v", control.calls)
		}
	})

	t.Run("controller errors are returned", func(t *testing.T) {
		control := &fakeController{err: errors.New("db down")}
		_, listener, _ := HubHealthListenerFactory(config.Config{HubHealthTopic: "gateway_health"}, control)
		if err := listener([][]byte{valid("h1")}); err == nil {
			t.Error("expected the controller error")
		}
	})
}
