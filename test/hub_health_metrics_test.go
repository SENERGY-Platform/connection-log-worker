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

package test

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/controller"
	"github.com/SENERGY-Platform/connection-log-worker/test/server"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	hubHealthStateMetric   = "connectionlog_worker_hub_health_state"
	hubHealthReportsMetric = "connectionlog_worker_hub_health_reports"
)

func TestHubHealthMetrics(t *testing.T) {
	wg := &sync.WaitGroup{}
	defer wg.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conf, err := config.Load("../config.json")
	if err != nil {
		t.Fatal(err)
	}
	conf.HubOfflineNotificationDelay = "-"
	conf.HubUnhealthyNotificationDelay = "-"
	conf.HubHealthStaleAge = "15m"
	mongoPort, _, err := server.MongoDB(ctx, wg)
	if err != nil {
		t.Fatal(err)
	}
	conf.MongoUrl = "mongodb://localhost:" + mongoPort

	// the controller creates the unique index on hub_id
	control, err := controller.New(ctx, conf)
	if err != nil {
		t.Fatal(err)
	}
	collector := control.HubHealthCollector()

	mongoClient, err := mongo.Connect(ctx, options.Client().ApplyURI(conf.MongoUrl))
	if err != nil {
		t.Fatal(err)
	}
	defer mongoClient.Disconnect(context.Background())
	healths := mongoClient.Database(conf.MongoDatabase).Collection(conf.HubHealthCollection)

	reset := func(t *testing.T, docs ...bson.M) {
		t.Helper()
		if _, err := healths.DeleteMany(ctx, bson.M{}); err != nil {
			t.Fatal(err)
		}
		for _, doc := range docs {
			if _, err := healths.InsertOne(ctx, doc); err != nil {
				t.Fatal(err)
			}
		}
	}
	report := func(hub string, state any, age time.Duration) bson.M {
		return bson.M{
			"hub_id":   hub,
			"state":    state,
			"reasons":  []string{"must not leak into metrics"},
			"report":   bson.M{"state": state},
			"received": time.Now().Add(-age),
		}
	}

	t.Run("fresh reports are exported per hub, stale ones only counted", func(t *testing.T) {
		reset(t,
			report("hub-undetermined", 0, time.Minute),
			report("hub-healthy", 1, time.Minute),
			report("hub-unhealthy", 2, time.Minute),
			report("hub-stale", 2, 20*time.Minute),
		)
		expected := `
# HELP connectionlog_worker_hub_health_state state of the latest health report per hub (0 undetermined, 1 healthy, 2 unhealthy); only reports that are not stale
# TYPE connectionlog_worker_hub_health_state gauge
connectionlog_worker_hub_health_state{hub_id="hub-healthy"} 1
connectionlog_worker_hub_health_state{hub_id="hub-undetermined"} 0
connectionlog_worker_hub_health_state{hub_id="hub-unhealthy"} 2
# HELP connectionlog_worker_hub_health_reports number of stored hub health reports by state (0, 1, 2) and "stale"
# TYPE connectionlog_worker_hub_health_reports gauge
connectionlog_worker_hub_health_reports{state="0"} 1
connectionlog_worker_hub_health_reports{state="1"} 1
connectionlog_worker_hub_health_reports{state="2"} 1
connectionlog_worker_hub_health_reports{state="stale"} 1
`
		if err := testutil.CollectAndCompare(collector, strings.NewReader(expected), hubHealthStateMetric, hubHealthReportsMetric); err != nil {
			t.Error(err)
		}
	})

	t.Run("empty collection still reports every state with 0", func(t *testing.T) {
		reset(t)
		expected := `
# HELP connectionlog_worker_hub_health_reports number of stored hub health reports by state (0, 1, 2) and "stale"
# TYPE connectionlog_worker_hub_health_reports gauge
connectionlog_worker_hub_health_reports{state="0"} 0
connectionlog_worker_hub_health_reports{state="1"} 0
connectionlog_worker_hub_health_reports{state="2"} 0
connectionlog_worker_hub_health_reports{state="stale"} 0
`
		if err := testutil.CollectAndCompare(collector, strings.NewReader(expected), hubHealthStateMetric, hubHealthReportsMetric); err != nil {
			t.Error(err)
		}
	})

	t.Run("deleted hub disappears on the next scrape", func(t *testing.T) {
		reset(t, report("hub-a", 2, time.Minute), report("hub-b", 1, time.Minute))
		if n := testutil.CollectAndCount(collector, hubHealthStateMetric); n != 2 {
			t.Fatalf("expected 2 series, got %d", n)
		}
		if _, err := healths.DeleteOne(ctx, bson.M{"hub_id": "hub-a"}); err != nil {
			t.Fatal(err)
		}
		if n := testutil.CollectAndCount(collector, hubHealthStateMetric); n != 1 {
			t.Errorf("expected 1 series, got %d", n)
		}
	})

	t.Run("an unreadable report fails the scrape", func(t *testing.T) {
		reset(t, report("hub-broken", "not a number", time.Minute))
		err := testutil.CollectAndCompare(collector, strings.NewReader(""), hubHealthStateMetric, hubHealthReportsMetric)
		if err == nil {
			t.Fatal("expected the scrape to fail")
		}
		if strings.Contains(err.Error(), "must not leak") || strings.Contains(err.Error(), "hub-broken") {
			t.Errorf("error leaks report content: %v", err)
		}
	})

	t.Run("served on /metrics", func(t *testing.T) {
		reset(t, report("hub-http", 2, time.Minute))
		srv := httptest.NewServer(control.MetricsHandler())
		defer srv.Close()
		expected := `
# HELP connectionlog_worker_hub_health_state state of the latest health report per hub (0 undetermined, 1 healthy, 2 unhealthy); only reports that are not stale
# TYPE connectionlog_worker_hub_health_state gauge
connectionlog_worker_hub_health_state{hub_id="hub-http"} 2
`
		if err := testutil.ScrapeAndCompare(srv.URL, strings.NewReader(expected), hubHealthStateMetric); err != nil {
			t.Error(err)
		}
	})
}
