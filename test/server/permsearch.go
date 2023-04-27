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
	"encoding/json"
	"errors"
	"fmt"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

func PermissionSearch(ctx context.Context, wg *sync.WaitGroup, zk string, elasticIp string) (hostPort string, ipAddress string, err error) {
	log.Println("start PermissionSearch")
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "ghcr.io/senergy-platform/permission-search:dev",
			Env: map[string]string{
				"KAFKA_URL":   zk,
				"ELASTIC_URL": "http://" + elasticIp + ":9200",
			},
			ExposedPorts:    []string{"8080/tcp"},
			WaitingFor:      wait.ForListeningPort("8080/tcp"),
			AlwaysPullImage: true,
		},
		Started: true,
	})
	if err != nil {
		return "", "", err
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		log.Println("DEBUG: remove container PermissionSearch", c.Terminate(context.Background()))
	}()

	ipAddress, err = c.ContainerIP(ctx)
	if err != nil {
		return "", "", err
	}
	temp, err := c.MappedPort(ctx, "8080/tcp")
	if err != nil {
		return "", "", err
	}
	hostPort = temp.Port()

	return hostPort, ipAddress, err
}

func Elasticsearch(ctx context.Context, wg *sync.WaitGroup) (hostPort string, ipAddress string, err error) {
	log.Println("start elasticsearch")
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "docker.elastic.co/elasticsearch/elasticsearch:7.6.1",
			Env: map[string]string{
				"discovery.type": "single-node",
				"path.data":      "/opt/elasticsearch/volatile/data",
				"path.logs":      "/opt/elasticsearch/volatile/logs",
			},
			Tmpfs: map[string]string{
				"/opt/elasticsearch/volatile/data": "rw",
				"/opt/elasticsearch/volatile/logs": "rw",
				"/tmp":                             "rw",
			},
			WaitingFor: wait.ForAll(
				wait.ForListeningPort("9200/tcp"),
				wait.ForNop(waitretry(1*time.Minute, func(ctx context.Context, target wait.StrategyTarget) error {
					host, err := target.Host(ctx)
					if err != nil {
						log.Println("host", err)
						return err
					}
					port, err := target.MappedPort(ctx, "9200/tcp")
					if err != nil {
						log.Println("port", err)
						return err
					}
					return tryElasticSearchConnection(host, port.Port())
				}))),
			ExposedPorts:    []string{"9200/tcp"},
			AlwaysPullImage: true,
		},
		Started: true,
	})
	if err != nil {
		return "", "", err
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		log.Println("DEBUG: remove container elasticsearch", c.Terminate(context.Background()))
	}()

	ipAddress, err = c.ContainerIP(ctx)
	if err != nil {
		return "", "", err
	}
	temp, err := c.MappedPort(ctx, "9200/tcp")
	if err != nil {
		return "", "", err
	}
	hostPort = temp.Port()

	err = tryElasticSearchConnection("localhost", hostPort)
	if err != nil {
		log.Println("ERROR: tryElasticSearchConnection(\"localhost\", hostPort)", err)
		return "", "", err
	}
	err = tryElasticSearchConnection(ipAddress, "9200")
	if err != nil {
		log.Println("ERROR: tryElasticSearchConnection(ipAddress, \"9200\")", err)
		return "", "", err
	}

	return hostPort, ipAddress, err
}

func tryElasticSearchConnection(ip string, port string) error {
	log.Println("try elastic connection to ", ip, port, "...")
	resp, err := http.Get("http://" + ip + ":" + port + "/_cluster/health?wait_for_status=green&timeout=50s")
	if err != nil {
		log.Println("ERROR:", err)
		return err
	}
	if resp.StatusCode >= 300 {
		temp, _ := io.ReadAll(resp.Body)
		err = errors.New("unexpected status code " + resp.Status)
		log.Println("ERROR:", err, "\n", string(temp))
		return err
	}
	result := map[string]interface{}{}
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		log.Println("ERROR:", err)
		return err
	}
	if result["number_of_nodes"] != float64(1) || result["status"] != "green" {
		err = fmt.Errorf("%#v", result)
		log.Println("ERROR:", err)
		return err
	}
	return err
}
