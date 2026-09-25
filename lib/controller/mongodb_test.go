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
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const replicaSetURL = "mongodb://mongo-0.mongo:27017,mongo-1.mongo:27017/?replicaSet=rs0&readPreference=primary"

func testMongoConfig() config.Config {
	return config.Config{
		MongoUrl:                                "mongodb://localhost:27017",
		MongoAuthSource:                         "admin",
		MongoDatabase:                           "connection_log",
		DeviceStateCollection:                   "devicestate",
		HubStateCollection:                      "gatewaystate",
		DeviceOfflineNotificationInfoCollection: "device_offline_notification_info",
		RoundTime:                               "1m",
	}
}

func TestMongoClientOptions_AuthWhenUserGiven(t *testing.T) {
	conf := testMongoConfig()
	conf.MongoUrl = replicaSetURL
	conf.MongoUser = "connection-log-worker"
	conf.MongoPassword = "s3cr3t"
	opts := mongoClientOptions(conf)
	if err := opts.Validate(); err != nil {
		t.Fatal(err)
	}
	want := &options.Credential{Username: "connection-log-worker", Password: "s3cr3t", AuthSource: "admin"}
	if !reflect.DeepEqual(opts.Auth, want) {
		t.Errorf("auth = %+v, want %+v", opts.Auth, want)
	}
}

func TestMongoClientOptions_NoAuthWhenUserEmpty(t *testing.T) {
	// A password without a user must not switch auth on.
	conf := testMongoConfig()
	conf.MongoPassword = "s3cr3t"
	opts := mongoClientOptions(conf)
	if err := opts.Validate(); err != nil {
		t.Fatal(err)
	}
	if opts.Auth != nil {
		t.Errorf("auth = %+v, want nil", opts.Auth)
	}
}

func TestMongoClientOptions_ConfiguredCredentialsReplaceURICredentials(t *testing.T) {
	conf := testMongoConfig()
	conf.MongoUrl = "mongodb://old:oldpw@localhost:27017/?authSource=other&authMechanism=SCRAM-SHA-1"
	conf.MongoUser = "connection-log-worker"
	conf.MongoPassword = "newpw"
	opts := mongoClientOptions(conf)
	if err := opts.Validate(); err != nil {
		t.Fatal(err)
	}
	want := &options.Credential{Username: "connection-log-worker", Password: "newpw", AuthSource: "admin"}
	if !reflect.DeepEqual(opts.Auth, want) {
		t.Errorf("auth = %+v, want %+v", opts.Auth, want)
	}
}

func TestMongoClientOptions_URIPassedUnchanged(t *testing.T) {
	conf := testMongoConfig()
	conf.MongoUrl = replicaSetURL
	opts := mongoClientOptions(conf)
	if err := opts.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := opts.GetURI(); got != replicaSetURL {
		t.Errorf("uri = %q, want %q", got, replicaSetURL)
	}
	if want := []string{"mongo-0.mongo:27017", "mongo-1.mongo:27017"}; !reflect.DeepEqual(opts.Hosts, want) {
		t.Errorf("hosts = %v, want %v", opts.Hosts, want)
	}
	if opts.ReplicaSet == nil || *opts.ReplicaSet != "rs0" {
		t.Errorf("replica set = %v, want rs0", opts.ReplicaSet)
	}
	// mgo.Monotonic has no counterpart; nothing but the URI may set a read preference.
	if opts.ReadPreference == nil || opts.ReadPreference.Mode().String() != "primary" {
		t.Errorf("read preference = %v, want primary from the URI", opts.ReadPreference)
	}
	plain := mongoClientOptions(testMongoConfig())
	if plain.ReadPreference != nil {
		t.Errorf("read preference = %v, want the driver default", plain.ReadPreference)
	}
}

// mgo.Dial accepted host:port; the driver needs the scheme, and none is added.
func TestMongoClientOptions_NoSchemeAdded(t *testing.T) {
	conf := testMongoConfig()
	conf.MongoUrl = "mongo:27017"
	if err := mongoClientOptions(conf).Validate(); err == nil {
		t.Fatal("expected an error for a url without scheme")
	}
}

func TestValidateMongoConfig(t *testing.T) {
	tests := []struct {
		name                     string
		database, user, password string
		wantErr                  error
	}{
		{"no auth", "connection_log", "", "", nil},
		{"user and password", "connection_log", "u", "p", nil},
		{"password without user", "connection_log", "", "p", nil},
		{"user without password", "connection_log", "u", "", errMissingPassword},
		{"empty database", "", "", "", errEmptyDatabase},
		{"empty database with auth", "", "u", "p", errEmptyDatabase},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conf := testMongoConfig()
			conf.MongoDatabase, conf.MongoUser, conf.MongoPassword = tt.database, tt.user, tt.password
			if err := validateMongoConfig(conf); !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// unreachableURL points at a port that was just free, so only the startup check can fail.
func unreachableURL(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	return "mongodb://" + addr + "/?directConnection=true"
}

// The startup check would fail as well, so these check for the specific validation error.
func TestNew_RejectsInvalidMongoConfigBeforeConnecting(t *testing.T) {
	cases := map[string]struct {
		database, user string
		want           error
	}{
		"empty database":        {"", "connection-log-worker", errEmptyDatabase},
		"user without password": {"connection_log", "connection-log-worker", errMissingPassword},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			conf := testMongoConfig()
			conf.MongoUrl = unreachableURL(t)
			conf.MongoDatabase, conf.MongoUser = c.database, c.user
			if c.want == errEmptyDatabase {
				conf.MongoPassword = "s3cr3t"
			}
			begin := time.Now()
			ctrl, err := New(context.Background(), conf)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if ctrl != nil {
				t.Error("expected no controller on failure")
			}
			if strings.Contains(err.Error(), "s3cr3t") {
				t.Errorf("error leaks the password: %v", err)
			}
			if elapsed := time.Since(begin); elapsed > time.Second {
				t.Errorf("validation took %v, a connection was attempted", elapsed)
			}
		})
	}
}

// poolCounter counts connection pools; Disconnect closes every pool Connect created.
type poolCounter struct{ created, closed atomic.Int32 }

func (p *poolCounter) monitor() *event.PoolMonitor {
	return &event.PoolMonitor{Event: func(e *event.PoolEvent) {
		switch e.Type {
		case event.PoolCreated:
			p.created.Add(1)
		case event.PoolClosedEvent:
			p.closed.Add(1)
		}
	}}
}

func (p *poolCounter) assertAllClosed(t *testing.T) {
	t.Helper()
	created, closed := p.created.Load(), p.closed.Load()
	if created == 0 || closed != created {
		t.Errorf("%d of %d connection pools closed, the client was left connected", closed, created)
	}
}

func TestStartMongo_StartupCheckFailsWithoutServer(t *testing.T) {
	const password = "pw-must-not-appear-7f3a"
	conf := testMongoConfig()
	conf.MongoUrl = unreachableURL(t)
	conf.MongoUser = "connection-log-worker"
	conf.MongoPassword = password
	pools := &poolCounter{}
	begin := time.Now()
	client, err := startMongo(context.Background(), conf, mongoClientOptions(conf).SetPoolMonitor(pools.monitor()), 500*time.Millisecond)
	if err == nil {
		t.Fatal("expected an error when the server is unreachable")
	}
	if client != nil {
		t.Error("expected no client on failure")
	}
	if !strings.HasPrefix(err.Error(), "mongo startup check failed: ") {
		t.Errorf("unexpected error: %v", err)
	}
	if strings.Contains(err.Error(), password) {
		t.Error("error text contains the password")
	}
	if elapsed := time.Since(begin); elapsed > 5*time.Second {
		t.Errorf("start took %v, the timeout was not applied", elapsed)
	}
	pools.assertAllClosed(t)
}

func TestInFilter_NilEncodesAsEmptyArray(t *testing.T) {
	for name, ids := range map[string][]string{"nil": nil, "empty": {}, "values": {"a", "b"}} {
		t.Run(name, func(t *testing.T) {
			b, err := bson.Marshal(inFilter("device", ids))
			if err != nil {
				t.Fatal(err)
			}
			in := bson.Raw(b).Lookup("device", "$in")
			if in.Type != bsontype.Array {
				t.Fatalf("$in is %v, want an array: %s", in.Type, bson.Raw(b))
			}
			values, err := in.Array().Values()
			if err != nil {
				t.Fatal(err)
			}
			if len(values) != len(ids) {
				t.Errorf("$in has %d values, want %d", len(values), len(ids))
			}
		})
	}
}

func TestDecodeAll_SkipsUndecodableDocuments(t *testing.T) {
	docs := []interface{}{
		bson.D{{Key: "device", Value: "ok"}, {Key: "online", Value: true}, {Key: "since", Value: int64(10)}},
		bson.D{{Key: "device", Value: "bad-online"}, {Key: "online", Value: "yes"}, {Key: "since", Value: int64(11)}},
		bson.D{{Key: "device", Value: "double-since"}, {Key: "online", Value: false}, {Key: "since", Value: 12.9}},
		bson.D{{Key: "device", Value: "int32-since"}, {Key: "online", Value: true}, {Key: "since", Value: int32(13)}},
		bson.D{{Key: "device", Value: "bad-since"}, {Key: "online", Value: true}, {Key: "since", Value: "14"}},
	}
	cursor, err := mongo.NewCursorFromDocuments(docs, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	got, err := decodeAll[DeviceState](context.Background(), cursor, slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	want := []DeviceState{
		{Device: "ok", Online: true, Since: 10},
		{Device: "double-since", Online: false, Since: 12},
		{Device: "int32-since", Online: true, Since: 13},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decoded %+v, want %+v", got, want)
	}
	if n := strings.Count(logs.String(), "skip undecodable mongo document"); n != 2 {
		t.Errorf("logged %d skipped documents, want 2: %s", n, logs)
	}
}

// mgo truncated a stored double into an int64 field; without the truncate tag the driver fails the decode.
func TestStateTypesTruncateDoubles(t *testing.T) {
	check := func(t *testing.T, doc bson.D, target interface{}, get func() int64) {
		t.Helper()
		b, err := bson.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if err = bson.Unmarshal(b, target); err != nil {
			t.Fatal(err)
		}
		if got := get(); got != 7 {
			t.Errorf("decoded %d, want 7", got)
		}
	}
	var d DeviceState
	check(t, bson.D{{Key: "since", Value: 7.8}}, &d, func() int64 { return d.Since })
	var h HubState
	check(t, bson.D{{Key: "since", Value: 7.8}}, &h, func() int64 { return h.Since })
	var n DeviceOfflineNotificationInfo
	check(t, bson.D{{Key: "offline_since", Value: 7.8}}, &n, func() int64 { return n.OfflineSince })
}

// These are the documents mgo wrote; the driver must produce the same bytes so stored data keeps its types.
func TestStateTypesEncodeLikeMgo(t *testing.T) {
	cases := []struct {
		value interface{}
		want  bson.D
	}{
		{DeviceState{Device: "d", Online: true, Since: 5}, bson.D{{Key: "device", Value: "d"}, {Key: "online", Value: true}, {Key: "since", Value: int64(5)}}},
		{DeviceState{}, bson.D{{Key: "online", Value: false}, {Key: "since", Value: int64(0)}}},
		{HubState{Gateway: "g", Since: 1 << 40}, bson.D{{Key: "gateway", Value: "g"}, {Key: "online", Value: false}, {Key: "since", Value: int64(1 << 40)}}},
		{DeviceOfflineNotificationInfo{DeviceId: "x", OfflineSince: 3, Notified: true}, bson.D{{Key: "device_id", Value: "x"}, {Key: "offline_since", Value: int64(3)}, {Key: "notified", Value: true}}},
	}
	for _, c := range cases {
		got, err := bson.Marshal(c.value)
		if err != nil {
			t.Fatal(err)
		}
		want, err := bson.Marshal(c.want)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%T encoded as %s, want %s", c.value, bson.Raw(got), bson.Raw(want))
		}
	}
}

func TestBulkWrite_EmptyBatchIsNoop(t *testing.T) {
	// mongo.Connect does not contact the server, so no running instance is needed.
	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(unreachableURL(t)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	if err = bulkWrite(client.Database("db").Collection("c"), nil); err != nil {
		t.Errorf("empty batch: %v", err)
	}
}

func TestCollectionsUseConfiguredDatabase(t *testing.T) {
	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	conf := testMongoConfig()
	conf.MongoDatabase = "custom_db"
	ctrl := &Controller{config: conf, mongo: client}
	for want, coll := range map[string]*mongo.Collection{
		"custom_db.devicestate":                      ctrl.getDeviceStateCollection(),
		"custom_db.gatewaystate":                     ctrl.getHubStateCollection(),
		"custom_db.device_offline_notification_info": ctrl.getDeviceOfflineNotificationInfoCollection(),
	} {
		if got := coll.Database().Name() + "." + coll.Name(); got != want {
			t.Errorf("collection = %s, want %s", got, want)
		}
	}
}
