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

package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type mongoFields struct {
	Url, User, Password, AuthSource, Database string
	DeviceState, HubState, Notification       string
}

func mongoOf(c Config) mongoFields {
	return mongoFields{
		c.MongoUrl, c.MongoUser, c.MongoPassword, c.MongoAuthSource, c.MongoDatabase,
		c.DeviceStateCollection, c.HubStateCollection, c.DeviceOfflineNotificationInfoCollection,
	}
}

// clearMongoEnv empties the variables for this test; the loader ignores empty values.
func clearMongoEnv(t *testing.T) {
	for _, k := range []string{"MONGO_URL", "MONGO_USER", "MONGO_PASSWORD", "MONGO_AUTH_SOURCE", "MONGO_DATABASE", "MONGO_TABLE",
		"DEVICE_STATE_COLLECTION", "HUB_STATE_COLLECTION", "DEVICE_OFFLINE_NOTIFICATION_INFO_COLLECTION"} {
		t.Setenv(k, "")
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// load discards the loader's stdout, which prints applied environment variables.
func load(t *testing.T, location string) Config {
	t.Helper()
	var cfg Config
	captureStdout(t, func() {
		var err error
		if cfg, err = Load(location); err != nil {
			t.Error(err)
		}
	})
	return cfg
}

func TestLoad_RepoConfigMongoDefaults(t *testing.T) {
	clearMongoEnv(t)
	want := mongoFields{
		Url: "mongodb://localhost:27017", AuthSource: "admin", Database: "connection_log",
		DeviceState: "devicestate", HubState: "gatewaystate", Notification: "device_offline_notification_info",
	}
	if got := mongoOf(load(t, "../../config.json")); got != want {
		t.Errorf("mongo = %+v, want %+v", got, want)
	}
}

func TestLoad_MongoDefaultsWhenFileOmitsThem(t *testing.T) {
	clearMongoEnv(t)
	want := mongoFields{Url: "mongodb://localhost:27017", AuthSource: "admin", Database: "connection_log"}
	if got := mongoOf(load(t, writeConfig(t, `{}`))); got != want {
		t.Errorf("mongo = %+v, want %+v", got, want)
	}
}

func TestLoad_MongoEnvNames(t *testing.T) {
	clearMongoEnv(t)
	t.Setenv("MONGO_URL", "mongodb://mongo-0.mongo:27017,mongo-1.mongo:27017/?replicaSet=rs0")
	t.Setenv("MONGO_USER", "connection-log-worker")
	t.Setenv("MONGO_PASSWORD", "p@ss:w/rd")
	t.Setenv("MONGO_AUTH_SOURCE", "users")
	t.Setenv("MONGO_DATABASE", "connectionlog")
	want := mongoFields{
		Url:          "mongodb://mongo-0.mongo:27017,mongo-1.mongo:27017/?replicaSet=rs0",
		User:         "connection-log-worker",
		Password:     "p@ss:w/rd",
		AuthSource:   "users",
		Database:     "connectionlog",
		DeviceState:  "devicestate",
		HubState:     "gatewaystate",
		Notification: "device_offline_notification_info",
	}
	if got := mongoOf(load(t, "../../config.json")); got != want {
		t.Errorf("mongo = %+v, want %+v", got, want)
	}
}

func TestLoad_MongoTableNoLongerRead(t *testing.T) {
	clearMongoEnv(t)
	t.Setenv("MONGO_TABLE", "other")
	cfg := load(t, writeConfig(t, `{"MongoTable": "other"}`))
	if cfg.MongoDatabase != "connection_log" {
		t.Errorf("database = %q, want connection_log", cfg.MongoDatabase)
	}
}

func TestLoad_MongoConfigFile(t *testing.T) {
	clearMongoEnv(t)
	cfg := load(t, writeConfig(t, `{"MongoUrl": "mongodb://file:27017", "MongoUser": "u", "MongoPassword": "s3cr3t", "MongoAuthSource": "a", "MongoDatabase": "d"}`))
	want := mongoFields{Url: "mongodb://file:27017", User: "u", Password: "s3cr3t", AuthSource: "a", Database: "d"}
	if got := mongoOf(cfg); got != want {
		t.Errorf("mongo = %+v, want %+v", got, want)
	}
}

// An explicitly empty database in the file is kept, so the startup validation can reject it.
func TestLoad_ExplicitEmptyDatabaseKept(t *testing.T) {
	clearMongoEnv(t)
	if cfg := load(t, writeConfig(t, `{"MongoDatabase": ""}`)); cfg.MongoDatabase != "" {
		t.Errorf("database = %q, want empty", cfg.MongoDatabase)
	}
}

// The loader prints every environment variable it applies except the ones tagged secret.
func TestLoad_EnvPrintMasksMongoPassword(t *testing.T) {
	clearMongoEnv(t)
	t.Setenv("MONGO_USER", "connection-log-worker")
	t.Setenv("MONGO_PASSWORD", "s3cr3t-pw")
	out := captureStdout(t, func() {
		if _, err := Load("../../config.json"); err != nil {
			t.Error(err)
		}
	})
	if strings.Contains(out, "s3cr3t-pw") {
		t.Errorf("printed environment leaks the password: %s", out)
	}
	if !strings.Contains(out, "MONGO_USER  =  connection-log-worker") {
		t.Errorf("expected the applied variables to be printed, got: %s", out)
	}
}

func TestConfigFormattingMasksMongoPassword(t *testing.T) {
	clearMongoEnv(t)
	t.Setenv("MONGO_PASSWORD", "s3cr3t-pw")
	cfg := load(t, "../../config.json")
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pb, err := json.Marshal(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	outputs := map[string]string{
		"json":         string(b),
		"json pointer": string(pb),
		"%v":           fmt.Sprintf("%v", cfg),
		"%+v":          fmt.Sprintf("%+v", cfg),
		"%#v":          fmt.Sprintf("%#v", cfg),
		"%s":           fmt.Sprintf("%s", cfg),
		"%v pointer":   fmt.Sprintf("%v", &cfg),
		"%+v pointer":  fmt.Sprintf("%+v", &cfg),
		"%#v pointer":  fmt.Sprintf("%#v", &cfg),
	}
	for name, s := range outputs {
		if strings.Contains(s, "s3cr3t-pw") {
			t.Errorf("%s leaks the password: %s", name, s)
		}
		if !strings.Contains(s, "devicestate") {
			t.Errorf("%s lost the other fields: %s", name, s)
		}
	}
	if !strings.Contains(string(b), `"MongoPassword":"***"`) {
		t.Errorf("json does not show the password as masked: %s", b)
	}
	if cfg.MongoPassword != "s3cr3t-pw" {
		t.Errorf("masking changed the loaded password to %q", cfg.MongoPassword)
	}
}

func TestConfigFormattingKeepsEmptyPasswordEmpty(t *testing.T) {
	b, err := json.Marshal(Config{MongoUser: "u"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"MongoPassword":""`) {
		t.Errorf("an empty password should stay visibly empty: %s", b)
	}
}

func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	defer func() { os.Stdout = orig }()
	f()
	_ = w.Close()
	return <-done
}
