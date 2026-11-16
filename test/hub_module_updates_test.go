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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/controller"
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	"github.com/SENERGY-Platform/connection-log-worker/test/server"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	hubModulesOutdatedMetric = "connectionlog_worker_hub_modules_outdated"
	hubHealthMissingMetric   = "connectionlog_worker_hub_health_missing"
)

// The module tests need no Kafka: reports are handed to the controller directly. TimescaleDB is only there
// because the controller creates the history tables and deleting a hub also clears its history. HubModuleOutdatedAge is one
// hour, so a report received two hours ago is outdated at once and one received now is not.
func TestHubModuleUpdates(t *testing.T) {
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
	conf.HubHealthTopic = "gateway_health" // no consumer is started here, the controller only needs to know it is enabled
	conf.HubModuleOutdatedAge = "1h"
	conf.HubModuleOutdatedNotificationInterval = "200ms"
	mongoPort, _, err := server.MongoDB(ctx, wg)
	if err != nil {
		t.Fatal(err)
	}
	conf.MongoUrl = "mongodb://localhost:" + mongoPort
	// DELETE also removes the hub's connection history from TimescaleDB
	if err = server.StartTimescale(ctx, wg, &conf); err != nil {
		t.Fatal(err)
	}

	notifier := &fakeNotifier{}
	var failSending atomic.Bool
	notifierServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failSending.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		notifier.ServeHTTP(w, r)
	}))
	defer notifierServer.Close()
	conf.NotificationUrl = notifierServer.URL

	repoServer := httptest.NewServer(http.HandlerFunc(fakeHubRepository))
	defer repoServer.Close()
	conf.DeviceRepositoryUrl = repoServer.URL

	control, err := controller.New(ctx, conf)
	if err != nil {
		t.Fatal(err)
	}

	mongoClient, err := mongo.Connect(ctx, options.Client().ApplyURI(conf.MongoUrl))
	if err != nil {
		t.Fatal(err)
	}
	defer mongoClient.Disconnect(context.Background())
	db := mongoClient.Database(conf.MongoDatabase)
	updates := db.Collection(conf.HubModuleUpdateInfoCollection)
	healths := db.Collection(conf.HubHealthCollection)
	states := db.Collection(conf.HubStateCollection)

	// module is "<id>" or "<id>:<next version>"
	sendReport := func(t *testing.T, hub string, at time.Time, modules string) {
		t.Helper()
		report := `{"version":1,"time":"` + at.Format(time.RFC3339) + `","state":1`
		if modules != "" {
			report += `,"module_versions":` + modules
		}
		report += `}`
		raw, err := json.Marshal(map[string]any{"hub_id": hub, "user_id": "user", "time": at, "report": json.RawMessage(report)})
		if err != nil {
			t.Fatal(err)
		}
		msg, err := model.ParseHubHealthMessage(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err = control.LogHubHealths([]model.HubHealthMessage{msg}); err != nil {
			t.Fatal(err)
		}
	}
	module := func(id string, next string) string {
		return `{"id":"` + id + `","name":"name of ` + id + `","source":"s","channel":"main","version":"v1","next_version":"` + next + `"}`
	}
	list := func(modules ...string) string { return "[" + strings.Join(modules, ",") + "]" }

	type doc struct {
		HubId        string `bson:"hub_id"`
		ModuleId     string `bson:"module_id"`
		Name         string `bson:"name"`
		Version      string `bson:"version"`
		NextVersion  string `bson:"next_version"`
		PendingSince int64  `bson:"pending_since"`
		Notified     bool   `bson:"notified"`
	}
	docs := func(t *testing.T, hub string) (result map[string]doc) {
		t.Helper()
		cursor, err := updates.Find(ctx, bson.M{"hub_id": hub})
		if err != nil {
			t.Fatal(err)
		}
		var all []doc
		if err = cursor.All(ctx, &all); err != nil {
			t.Fatal(err)
		}
		result = map[string]doc{}
		for _, d := range all {
			result[d.ModuleId] = d
		}
		return result
	}
	stay := func() { time.Sleep(2 * time.Second) }
	// the history database may refuse connections for a moment after its port is open
	retry := func(t *testing.T, f func() error) {
		t.Helper()
		var err error
		eventually(t, 30*time.Second, func() bool { err = f(); return err == nil })
	}

	t.Run("scenarios", func(t *testing.T) {
		t.Run("one notification per hub for several modules", func(t *testing.T) {
			t.Parallel()
			id := "hub-several"
			start := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
			sendReport(t, id, start, list(module("github.com/x/b", "v2"), module("github.com/x/a", "v3"), module("github.com/x/c", "")))
			if got := docs(t, id); len(got) != 2 || got["github.com/x/a"].PendingSince != start.Unix() || got["github.com/x/c"].ModuleId != "" {
				t.Fatalf("unexpected tracking %#v", got)
			}
			eventually(t, 30*time.Second, func() bool { return len(notifier.forHub(id)) == 1 })
			n := notifier.forHub(id)[0]
			expected := "network name of " + id + " (" + id + ") has module updates pending since " + start.UTC().Format(time.RFC3339) +
				": name of github.com/x/a v1 -> v3; name of github.com/x/b v1 -> v2"
			if n.UserId != hubOfflineTestOwner || n.Topic != "mgw" || n.Title != "Network Modules Outdated" || n.Message != expected {
				t.Errorf("unexpected notification %#v", n)
			}
			for module, d := range docs(t, id) {
				if !d.Notified {
					t.Errorf("%s must be marked as notified", module)
				}
			}
			stay()
			if got := notifier.forHub(id); len(got) != 1 {
				t.Errorf("expected exactly one notification, got %#v", got)
			}
		})

		t.Run("not due yet", func(t *testing.T) {
			t.Parallel()
			id := "hub-not-due"
			sendReport(t, id, time.Now(), list(module("github.com/x/a", "v2")))
			stay()
			if got := notifier.forHub(id); len(got) != 0 {
				t.Errorf("expected no notification, got %#v", got)
			}
			if d := docs(t, id)["github.com/x/a"]; d.Notified || d.PendingSince == 0 {
				t.Errorf("unexpected tracking %#v", d)
			}
		})

		t.Run("a newer next version does not notify again", func(t *testing.T) {
			t.Parallel()
			id := "hub-newer-next"
			start := time.Now().Add(-4 * time.Hour).Truncate(time.Second)
			sendReport(t, id, start, list(module("github.com/x/a", "v2")))
			eventually(t, 30*time.Second, func() bool { return len(notifier.forHub(id)) == 1 })
			sendReport(t, id, start.Add(time.Hour), list(module("github.com/x/a", "v3")))
			d := docs(t, id)["github.com/x/a"]
			if d.NextVersion != "v3" || d.PendingSince != start.Unix() || !d.Notified {
				t.Errorf("expected the next version to change and the period to stay, got %#v", d)
			}
			stay()
			if got := notifier.forHub(id); len(got) != 1 {
				t.Errorf("expected exactly one notification, got %#v", got)
			}
		})

		t.Run("a new period after the module was updated", func(t *testing.T) {
			t.Parallel()
			id := "hub-new-period"
			start := time.Now().Add(-5 * time.Hour).Truncate(time.Second)
			sendReport(t, id, start, list(module("github.com/x/a", "v2")))
			eventually(t, 30*time.Second, func() bool { return len(notifier.forHub(id)) == 1 })
			sendReport(t, id, start.Add(time.Hour), list(module("github.com/x/a", "")))
			if got := docs(t, id); len(got) != 0 {
				t.Fatalf("expected the tracking to end, got %#v", got)
			}
			again := start.Add(2 * time.Hour)
			sendReport(t, id, again, list(module("github.com/x/a", "v3")))
			if d := docs(t, id)["github.com/x/a"]; d.Notified || d.PendingSince != again.Unix() {
				t.Fatalf("expected a new period, got %#v", d)
			}
			eventually(t, 30*time.Second, func() bool { return len(notifier.forHub(id)) == 2 })
			if m := notifier.forHub(id)[1].Message; !strings.Contains(m, "pending since "+again.UTC().Format(time.RFC3339)) || !strings.HasSuffix(m, "v1 -> v3") {
				t.Errorf("unexpected second notification %q", m)
			}
		})

		t.Run("a module that is no longer listed ends its period", func(t *testing.T) {
			t.Parallel()
			id := "hub-unlisted"
			start := time.Now()
			sendReport(t, id, start, list(module("github.com/x/a", "v2"), module("github.com/x/b", "v2")))
			sendReport(t, id, start.Add(time.Second), list(module("github.com/x/b", "v2")))
			got := docs(t, id)
			if _, ok := got["github.com/x/a"]; ok || len(got) != 1 {
				t.Errorf("unexpected tracking %#v", got)
			}
		})

		t.Run("reports without a usable module list change nothing", func(t *testing.T) {
			t.Parallel()
			id := "hub-unknown-list"
			start := time.Now()
			sendReport(t, id, start, list(module("github.com/x/a", "v2")))
			sendReport(t, id, start.Add(time.Second), "")
			sendReport(t, id, start.Add(2*time.Second), "null")
			sendReport(t, id, start.Add(3*time.Second), `{"not":"a list"}`)
			if d := docs(t, id)["github.com/x/a"]; d.NextVersion != "v2" || d.PendingSince != start.Unix() {
				t.Errorf("expected the tracking to stay, got %#v", d)
			}
			var stored struct {
				ReportTime string `bson:"report_time"`
			}
			if err := healths.FindOne(ctx, bson.M{"hub_id": id}).Decode(&stored); err != nil || stored.ReportTime != start.Add(3*time.Second).Format(time.RFC3339) {
				t.Errorf("the report with the malformed list must be stored, got %v %v", stored, err)
			}
		})

		t.Run("a report the store rejected does not touch the tracking", func(t *testing.T) {
			t.Parallel()
			id := "hub-rejected"
			now := time.Now()
			sendReport(t, id, now, list(module("github.com/x/a", "v2")))
			sendReport(t, id, now.Add(-time.Hour), list())
			sendReport(t, id, now.Add(-2*time.Hour), list(module("github.com/x/b", "v2")))
			got := docs(t, id)
			if len(got) != 1 || got["github.com/x/a"].PendingSince != now.Unix() {
				t.Errorf("an older report must not change the tracking, got %#v", got)
			}
		})

		t.Run("inactive hub", func(t *testing.T) {
			t.Parallel()
			id := "inactive-hub-modules"
			sendReport(t, id, time.Now().Add(-3*time.Hour), list(module("github.com/x/a", "v2")))
			eventually(t, 30*time.Second, func() bool { return hubReadCount(id) > 0 })
			stay()
			if got := notifier.forHub(id); len(got) != 0 {
				t.Errorf("expected no notification, got %#v", got)
			}
			if n := hubReadCount(id); n != 1 {
				t.Errorf("expected the hub to be read once, got %d", n)
			}
			if d := docs(t, id)["github.com/x/a"]; !d.Notified {
				t.Errorf("expected the claim to stay, got %#v", d)
			}
		})

		t.Run("hub ignoring module updates", func(t *testing.T) {
			t.Parallel()
			id := "ignore-modules-hub"
			sendReport(t, id, time.Now().Add(-3*time.Hour), list(module("github.com/x/a", "v2")))
			eventually(t, 30*time.Second, func() bool { return hubReadCount(id) > 0 })
			stay()
			if got := notifier.forHub(id); len(got) != 0 {
				t.Errorf("expected no notification, got %#v", got)
			}
			if n := hubReadCount(id); n != 1 {
				t.Errorf("expected the hub to be read once, got %d", n)
			}
			if d := docs(t, id)["github.com/x/a"]; !d.Notified {
				t.Errorf("expected the claim to stay, got %#v", d)
			}
		})

		t.Run("unknown to the device-repository", func(t *testing.T) {
			t.Parallel()
			id := "unknown-hub-modules"
			sendReport(t, id, time.Now().Add(-3*time.Hour), list(module("github.com/x/a", "v2")))
			eventually(t, 30*time.Second, func() bool { return len(docs(t, id)) == 0 })
			if got := notifier.forHub(id); len(got) != 0 {
				t.Errorf("expected no notification, got %#v", got)
			}
		})

		t.Run("a failed send releases the claim", func(t *testing.T) {
			// not parallel: the notifier fails for every hub while it runs
			id := "hub-send-fails"
			failSending.Store(true)
			sendReport(t, id, time.Now().Add(-3*time.Hour), list(module("github.com/x/a", "v2")))
			eventually(t, 30*time.Second, func() bool { return hubReadCount(id) >= 2 })
			failSending.Store(false)
			if got := notifier.forHub(id); len(got) != 0 {
				t.Errorf("expected no notification yet, got %#v", got)
			}
			eventually(t, 30*time.Second, func() bool { return len(notifier.forHub(id)) == 1 })
		})

		t.Run("deleted hub", func(t *testing.T) {
			t.Parallel()
			id := "hub-deleted-modules"
			sendReport(t, id, time.Now(), list(module("github.com/x/a", "v2"), module("github.com/x/b", "v2")))
			retry(t, func() error { return control.UpdateHub(model.HubCommand{Command: "DELETE", Id: id}) })
			if got := docs(t, id); len(got) != 0 {
				t.Errorf("expected the tracking to be removed, got %#v", got)
			}
		})

		t.Run("deleted hubs in a batch", func(t *testing.T) {
			t.Parallel()
			a, b := "hub-deleted-batch-a", "hub-deleted-batch-b"
			sendReport(t, a, time.Now(), list(module("github.com/x/a", "v2")))
			sendReport(t, b, time.Now(), list(module("github.com/x/a", "v2")))
			retry(t, func() error {
				return control.UpdateHubs([]model.HubCommand{{Command: "DELETE", Id: a}, {Command: "DELETE", Id: b}})
			})
			if len(docs(t, a)) != 0 || len(docs(t, b)) != 0 {
				t.Error("expected the tracking to be removed")
			}
		})
	})

	t.Run("gauges", func(t *testing.T) {
		collector := control.HubHealthCollector()
		clean := func(t *testing.T) {
			t.Helper()
			for _, c := range []*mongo.Collection{updates, healths, states, db.Collection(conf.HubInactiveCollection), db.Collection(conf.HubIgnoreModuleUpdatesCollection)} {
				if _, err := c.DeleteMany(ctx, bson.M{}); err != nil {
					t.Fatal(err)
				}
			}
		}
		clean(t)
		defer clean(t)

		// a hub that has been online for a long time, one that came online just now
		onlineSince := func(hub string, ago time.Duration) {
			if _, err := states.InsertOne(ctx, bson.M{"gateway": hub, "online": true, "since": time.Now().Add(-ago).Unix()}); err != nil {
				t.Fatal(err)
			}
		}
		onlineSince("gauge-missing", time.Hour)
		onlineSince("gauge-with-report", time.Hour)
		onlineSince("gauge-just-online", time.Minute)
		onlineSince("gauge-inactive", time.Hour)
		if _, err := states.InsertOne(ctx, bson.M{"gateway": "gauge-offline", "online": false, "since": time.Now().Add(-time.Hour).Unix()}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Collection(conf.HubInactiveCollection).InsertOne(ctx, bson.M{"hub_id": "gauge-inactive"}); err != nil {
			t.Fatal(err)
		}
		sendReport(t, "gauge-with-report", time.Now(), "")

		expected := `
# HELP connectionlog_worker_hub_health_missing 1 for hubs that have been online for longer than HubHealthStaleAge and have no stored health report; not of inactive hubs, only exported if the health consumer is enabled
# TYPE connectionlog_worker_hub_health_missing gauge
connectionlog_worker_hub_health_missing{hub_id="gauge-missing"} 1
`
		if err := testutil.CollectAndCompare(collector, strings.NewReader(expected), hubHealthMissingMetric); err != nil {
			t.Error(err)
		}

		// the report arrives: the series disappears
		sendReport(t, "gauge-missing", time.Now(), "")
		if n := testutil.CollectAndCount(collector, hubHealthMissingMetric); n != 0 {
			t.Errorf("expected no series once the report is stored, got %d", n)
		}

		sendReport(t, "gauge-outdated", time.Now().Add(-3*time.Hour), list(module("github.com/x/a", "v2"), module("github.com/x/b", "v2")))
		sendReport(t, "gauge-young", time.Now(), list(module("github.com/x/a", "v2")))
		sendReport(t, "gauge-inactive", time.Now().Add(-3*time.Hour), list(module("github.com/x/a", "v2")))
		if _, err := db.Collection(conf.HubIgnoreModuleUpdatesCollection).InsertOne(ctx, bson.M{"hub_id": "gauge-ignoring"}); err != nil {
			t.Fatal(err)
		}
		sendReport(t, "gauge-ignoring", time.Now().Add(-3*time.Hour), list(module("github.com/x/a", "v2")))
		expected = `
# HELP connectionlog_worker_hub_modules_outdated number of modules per hub whose update has been pending for longer than HubModuleOutdatedAge; only hubs with at least one, not of inactive hubs
# TYPE connectionlog_worker_hub_modules_outdated gauge
connectionlog_worker_hub_modules_outdated{hub_id="gauge-outdated"} 2
`
		if err := testutil.CollectAndCompare(collector, strings.NewReader(expected), hubModulesOutdatedMetric); err != nil {
			t.Error(err)
		}
	})
}
