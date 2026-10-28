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
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
)

func TestParseHubHealthStaleAge(t *testing.T) {
	for value, want := range map[string]time.Duration{"": 15 * time.Minute, "15m": 15 * time.Minute, "90s": 90 * time.Second} {
		got, err := parseHubHealthStaleAge(config.Config{HubHealthStaleAge: value})
		if err != nil || got != want {
			t.Errorf("%q: got %v, %v, want %v", value, got, err, want)
		}
	}
	for _, value := range []string{"garbage", "15", "0s", "-5m"} {
		if _, err := parseHubHealthStaleAge(config.Config{HubHealthStaleAge: value}); err == nil {
			t.Errorf("%q: expected an error", value)
		}
	}
}

func TestNewRejectsInvalidHubHealthStaleAge(t *testing.T) {
	conf := testMongoConfig()
	conf.HubHealthStaleAge = "garbage"
	// validation runs before the connection to MongoDB is made
	if _, err := New(t.Context(), conf); err == nil || !strings.Contains(err.Error(), "HubHealthStaleAge") {
		t.Errorf("expected a HubHealthStaleAge error, got %v", err)
	}
}
