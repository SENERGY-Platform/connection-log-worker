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
	"crypto/tls"
	"errors"
	"github.com/opensearch-project/opensearch-go"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"log"
	"net/http"
	"sync"
	"time"
)

func PermissionSearch(ctx context.Context, wg *sync.WaitGroup, kafkaUrl string, dbIp string) (hostPort string, ipAddress string, err error) {
	log.Println("start PermissionSearch")
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "ghcr.io/senergy-platform/permission-search:dev",
			Env: map[string]string{
				"KAFKA_URL":                        kafkaUrl,
				"OPEN_SEARCH_URLS":                 "https://" + dbIp + ":9200",
				"OPEN_SEARCH_USERNAME":             "admin",
				"OPEN_SEARCH_PASSWORD":             "admin",
				"OPEN_SEARCH_INSECURE_SKIP_VERIFY": "true",
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

func OpenSearch(ctx context.Context, wg *sync.WaitGroup) (hostPort string, ipAddress string, err error) {
	log.Println("start opensearch")
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "public.ecr.aws/opensearchproject/opensearch:2.8.0",
			Env: map[string]string{
				"discovery.type": "single-node",
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
					return tryOpenSearchConnection(host, port.Port())
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
		log.Println("DEBUG: remove container opensearch", c.Terminate(context.Background()))
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

	err = tryOpenSearchConnection("localhost", hostPort)
	if err != nil {
		log.Println("ERROR: tryOpenSearchConnection(\"localhost\", hostPort)", err)
		return "", "", err
	}
	err = tryOpenSearchConnection(ipAddress, "9200")
	if err != nil {
		log.Println("ERROR: tryOpenSearchConnection(ipAddress, \"9200\")", err)
		return "", "", err
	}

	return hostPort, ipAddress, err
}

func tryOpenSearchConnection(ip string, port string) error {
	log.Println("try opensearch connection to ", ip, port, "...")
	client, err := opensearch.NewClient(opensearch.Config{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Addresses: []string{"https://" + ip + ":" + port},
		Username:  "admin", // For testing only. Don't store credentials in code.
		Password:  "admin",
	})
	if err != nil {
		return err
	}
	resp, err := client.Cluster.Health(client.Cluster.Health.WithWaitForStatus("green"))
	if err != nil {
		return err
	}
	if resp.IsError() {
		return errors.New(resp.String())
	}
	return err
}
