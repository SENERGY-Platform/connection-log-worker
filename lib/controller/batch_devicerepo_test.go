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

package controller

import (
	"context"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	"github.com/SENERGY-Platform/connection-log-worker/test/server"
	devicerepo "github.com/SENERGY-Platform/device-repository/v2/lib/client"
	"github.com/google/uuid"
)

// A correction from connection-check repeats a state the worker already stores, because the device-repository holds a
// different one. The batch paths have to write it to the device-repository anyway, without a second history row.
func TestBatchRepeatedStateReachesDeviceRepository(t *testing.T) {
	wg := &sync.WaitGroup{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})
	ctrl := newHistoryController(t)

	mongoPort, _, err := server.MongoDB(ctx, wg)
	if err != nil {
		t.Fatal(err)
	}
	conf := testMongoConfig()
	conf.MongoUrl = "mongodb://localhost:" + mongoPort
	if err = server.SetPostgres(&conf, ctrl.config.PostgresHost, strconv.FormatInt(ctrl.config.PostgresPort, 10)); err != nil {
		t.Fatal(err)
	}
	ctrl.config = conf
	ctrl.mongo, err = newMongoClient(ctx, ctrl.config)
	if err != nil {
		t.Fatal(err)
	}

	repo := httptest.NewServer(server.DeviceRepo)
	t.Cleanup(repo.Close)
	ctrl.config.DeviceRepositoryUrl = repo.URL
	ctrl.deviceRepo = devicerepo.NewClient(repo.URL, nil)

	deviceId := "urn:infai:ses:device:" + uuid.NewString()
	hubId := "urn:infai:ses:hub:" + uuid.NewString()
	// older than an hour, so the device offline notification path stays out of this test
	at := time.Now().Add(-2 * time.Hour)

	for round, ts := range []time.Time{at, at.Add(time.Minute)} {
		if err = ctrl.LogDevices([]model.DeviceLog{{Id: deviceId, Connected: false, Time: ts}}); err != nil {
			t.Fatal(err)
		}
		if err = ctrl.LogHubs([]model.HubLog{{Id: hubId, Connected: false, Time: ts}}); err != nil {
			t.Fatal(err)
		}
		for kind, id := range map[string]string{"device": deviceId, "hub": hubId} {
			state, writes := server.DeviceRepo.ConnectionState(id)
			if writes != round+1 || state {
				t.Errorf("round %d: %s in device-repository: %d writes, online=%v; want %d writes, online=false", round+1, kind, writes, state, round+1)
			}
		}
	}

	if n := countRows(t, ctrl.db, deviceTable, deviceId); n != 1 {
		t.Errorf("device history rows = %d, want 1", n)
	}
	if n := countRows(t, ctrl.db, gatewayTable, hubId); n != 1 {
		t.Errorf("hub history rows = %d, want 1", n)
	}
}
