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
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/SENERGY-Platform/connection-log-worker/lib/config"
	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// defaultHubHealthStaleAge applies when HubHealthStaleAge is empty.
const defaultHubHealthStaleAge = 15 * time.Minute

// hubHealthMetricsTimeout bounds the Mongo query of one scrape.
const hubHealthMetricsTimeout = 10 * time.Second

// hubHealthStaleLabel is the value of the state label that counts the stale reports.
const hubHealthStaleLabel = "stale"

var (
	hubHealthStateDesc = prometheus.NewDesc(
		"connectionlog_worker_hub_health_state",
		"state of the latest health report per hub (0 undetermined, 1 healthy, 2 unhealthy); only reports that are not stale",
		[]string{"hub_id"}, nil)
	hubHealthReportsDesc = prometheus.NewDesc(
		"connectionlog_worker_hub_health_reports",
		"number of stored hub health reports by state (0, 1, 2) and \"stale\"",
		[]string{"state"}, nil)
)

// parseHubHealthStaleAge validates HubHealthStaleAge; empty selects the default.
func parseHubHealthStaleAge(conf config.Config) (time.Duration, error) {
	if conf.HubHealthStaleAge == "" {
		return defaultHubHealthStaleAge, nil
	}
	age, err := time.ParseDuration(conf.HubHealthStaleAge)
	if err != nil {
		return 0, fmt.Errorf("invalid HubHealthStaleAge: %w", err)
	}
	if age <= 0 {
		return 0, fmt.Errorf("invalid HubHealthStaleAge: %v must be positive", age)
	}
	return age, nil
}

// MetricsHandler serves the metrics of this controller from a registry of its own.
func (this *Controller) MetricsHandler() http.Handler {
	registry := prometheus.NewRegistry()
	registry.MustRegister(this.HubHealthCollector())
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{Registry: registry})
}

// HubHealthCollector reads the stored health reports on every scrape instead of keeping its own state,
// so deleted hubs disappear without cleanup.
func (this *Controller) HubHealthCollector() prometheus.Collector {
	return &hubHealthCollector{controller: this}
}

type hubHealthCollector struct {
	controller *Controller
}

func (this *hubHealthCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- hubHealthStateDesc
	ch <- hubHealthReportsDesc
}

func (this *hubHealthCollector) Collect(ch chan<- prometheus.Metric) {
	reports, err := this.controller.getHubHealthMetricReports()
	if err != nil {
		// the error may carry document content, so only the scrape fails visibly and the log stays generic
		this.controller.config.GetLogger().Error("unable to collect hub health metrics")
		ch <- prometheus.NewInvalidMetric(hubHealthStateDesc, err)
		ch <- prometheus.NewInvalidMetric(hubHealthReportsDesc, err)
		return
	}
	counts := map[string]int{"0": 0, "1": 0, "2": 0, hubHealthStaleLabel: 0}
	now := time.Now()
	for _, report := range reports {
		if now.Sub(report.Received) > this.controller.hubHealthStaleAge {
			counts[hubHealthStaleLabel]++
			continue
		}
		if report.State < model.HubHealthUndetermined || report.State > model.HubHealthUnhealthy {
			continue
		}
		counts[strconv.Itoa(report.State)]++
		ch <- prometheus.MustNewConstMetric(hubHealthStateDesc, prometheus.GaugeValue, float64(report.State), report.HubId)
	}
	for label, count := range counts {
		ch <- prometheus.MustNewConstMetric(hubHealthReportsDesc, prometheus.GaugeValue, float64(count), label)
	}
}

// getHubHealthMetricReports loads only the fields the metrics need.
func (this *Controller) getHubHealthMetricReports() ([]HubHealth, error) {
	ctx, cancel := context.WithTimeout(context.Background(), hubHealthMetricsTimeout)
	defer cancel()
	opts := options.Find().SetProjection(bson.M{"_id": 0, "hub_id": 1, "state": 1, "received": 1})
	cursor, err := this.getHubHealthCollection().Find(ctx, bson.M{}, opts)
	if err != nil {
		return nil, err
	}
	var result []HubHealth
	err = cursor.All(ctx, &result)
	return result, err
}
