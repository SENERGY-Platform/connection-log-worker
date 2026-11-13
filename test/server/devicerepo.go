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

package server

import (
	"encoding/json"
	"net/http"
	"sync"
)

// DeviceRepo stands in for the device-repository and records the batch connection-state writes it receives.
var DeviceRepo = &DeviceRepoStub{states: map[string]bool{}, writes: map[string]int{}}

type DeviceRepoStub struct {
	mux    sync.Mutex
	states map[string]bool
	writes map[string]int
}

func (this *DeviceRepoStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut && (r.URL.Path == "/devices-batch/connection-state" || r.URL.Path == "/hubs-batch/connection-state") {
		states := map[string]bool{}
		if err := json.NewDecoder(r.Body).Decode(&states); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		this.mux.Lock()
		for id, state := range states {
			this.states[id] = state
			this.writes[id]++
		}
		this.mux.Unlock()
	}
	w.WriteHeader(http.StatusOK)
}

// ConnectionState returns the last state written for id and how many batch writes contained it.
func (this *DeviceRepoStub) ConnectionState(id string) (state bool, writes int) {
	this.mux.Lock()
	defer this.mux.Unlock()
	return this.states[id], this.writes[id]
}
