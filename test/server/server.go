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

package server

import (
	"context"
	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"log"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"sync"
)

func New(ctx context.Context, wg *sync.WaitGroup, defaults config.Config) (config config.Config, connectionlogip string, err error) {
	config, err = NewPartial(ctx, wg, defaults)
	if err != nil {
		return config, connectionlogip, err
	}

	_, permV2Ip, err := PermissionsV2(ctx, wg, config.MongoUrl, config.KafkaUrl)
	if err != nil {
		log.Println("ERROR:", err)
		debug.PrintStack()
		return config, "", err
	}
	permissionsV2Url := "http://" + permV2Ip + ":8080"

	_, connectionlogip, err = Connectionlog(ctx, wg, config.MongoUrl, config.InfluxdbUrl, permissionsV2Url)
	if err != nil {
		log.Println("ERROR:", err)
		debug.PrintStack()
		return config, "", err
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer server.Close()
		<-ctx.Done()
	}()
	config.DeviceRepositoryUrl = server.URL

	return config, connectionlogip, nil
}

func NewPartial(ctx context.Context, wg *sync.WaitGroup, defaults config.Config) (config config.Config, err error) {
	config = defaults

	_, zk, err := Zookeeper(ctx, wg)
	if err != nil {
		log.Println("ERROR:", err)
		debug.PrintStack()
		return config, err
	}
	zkUrl := zk + ":2181"

	config.KafkaUrl, err = Kafka(ctx, wg, zkUrl)
	if err != nil {
		log.Println("ERROR:", err)
		debug.PrintStack()
		return config, err
	}

	_, influxip, err := Influxdb(ctx, wg)
	if err != nil {
		log.Println("ERROR:", err)
		debug.PrintStack()
		return config, err
	}
	config.InfluxdbUrl = "http://" + influxip + ":8086"
	config.InfluxdbDb = "connectionlog"
	config.InfluxdbUser = "user"
	config.InfluxdbPw = "pw"
	config.InfluxdbTimeout = 3

	_, mongoIp, err := MongoDB(ctx, wg)
	if err != nil {
		log.Println("ERROR:", err)
		debug.PrintStack()
		return config, err
	}
	config.MongoUrl = "mongodb://" + mongoIp

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer server.Close()
		<-ctx.Done()
	}()
	config.DeviceRepositoryUrl = server.URL

	return config, nil
}
