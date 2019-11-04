/*
 * Copyright 2019 InfAI (CC SES)
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
	"github.com/SENERGY-Platform/connection-log-worker/lib"
	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/source/consumer"
	"github.com/SENERGY-Platform/connection-log-worker/test/server"
	"log"
	"testing"
	"time"
)

func TestInit(t *testing.T) {
	defaultConfig, err := config.Load("../config.json")
	if err != nil {
		t.Error(err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer time.Sleep(10 * time.Second) //wait for docker cleanup
	defer cancel()

	config, connectionlog, err := server.New(ctx, defaultConfig)
	if err != nil {
		t.Error(err)
		return
	}

	err = lib.Start(ctx, config, func(err error, consumer *consumer.Consumer) {
		t.Error(err)
		return
	})
	if err != nil {
		t.Error(err)
		return
	}

	log.Println(connectionlog)
}
