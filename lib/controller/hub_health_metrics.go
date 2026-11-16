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

// hubHealthStaleLabel and hubHealthInactiveLabel are the values of the state label that count the stale
// reports and the reports of inactive hubs.
const (
	hubHealthStaleLabel    = "stale"
	hubHealthInactiveLabel = "inactive"
)

var (
	hubHealthStateDesc = prometheus.NewDesc(
		"connectionlog_worker_hub_health_state",
		"state of the latest health report per hub (0 undetermined, 1 healthy, 2 unhealthy); only reports that are not stale",
		[]string{"hub_id"}, nil)
	hubHealthStaleStateDesc = prometheus.NewDesc(
		"connectionlog_worker_hub_health_stale_state",
		"state of the latest health report per hub (0 undetermined, 1 healthy, 2 unhealthy); only reports that are stale, not of inactive hubs",
		[]string{"hub_id"}, nil)
	hubHealthReportsDesc = prometheus.NewDesc(
		"connectionlog_worker_hub_health_reports",
		"number of stored hub health reports by state (0, 1, 2), \"stale\" and \"inactive\"; the last two are not counted in 0, 1, 2",
		[]string{"state"}, nil)
	hubModulesOutdatedDesc = prometheus.NewDesc(
		"connectionlog_worker_hub_modules_outdated",
		"number of modules per hub whose update has been pending for longer than HubModuleOutdatedAge; only hubs with at least one, not of inactive hubs",
		[]string{"hub_id"}, nil)
	hubHealthMissingDesc = prometheus.NewDesc(
		"connectionlog_worker_hub_health_missing",
		"1 for hubs that have been online for longer than HubHealthStaleAge and have no stored health report; not of inactive hubs, only exported if the health consumer is enabled",
		[]string{"hub_id"}, nil)
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
	ch <- hubHealthStaleStateDesc
	ch <- hubHealthReportsDesc
	ch <- hubModulesOutdatedDesc
	ch <- hubHealthMissingDesc
}

func (this *hubHealthCollector) Collect(ch chan<- prometheus.Metric) {
	data, err := this.controller.getHubHealthMetricData()
	if err != nil {
		// the error may carry document content, so only the scrape fails visibly and the log stays generic
		this.controller.config.GetLogger().Error("unable to collect hub health metrics")
		ch <- prometheus.NewInvalidMetric(hubHealthStateDesc, err)
		ch <- prometheus.NewInvalidMetric(hubHealthStaleStateDesc, err)
		ch <- prometheus.NewInvalidMetric(hubHealthReportsDesc, err)
		ch <- prometheus.NewInvalidMetric(hubModulesOutdatedDesc, err)
		ch <- prometheus.NewInvalidMetric(hubHealthMissingDesc, err)
		return
	}
	reports, inactive := data.reports, data.inactive
	counts := map[string]int{"0": 0, "1": 0, "2": 0, hubHealthStaleLabel: 0, hubHealthInactiveLabel: 0}
	now := time.Now()
	for _, report := range reports {
		if inactive[report.HubId] {
			counts[hubHealthInactiveLabel]++
			continue
		}
		if now.Sub(report.Received) > this.controller.hubHealthStaleAge {
			counts[hubHealthStaleLabel]++
			if report.State >= model.HubHealthUndetermined && report.State <= model.HubHealthUnhealthy {
				// keeps the last state of a hub that stopped reporting, so an alert on unhealthy hubs outlives the gateway going offline
				ch <- prometheus.MustNewConstMetric(hubHealthStaleStateDesc, prometheus.GaugeValue, float64(report.State), report.HubId)
			}
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
	for hubId, count := range data.outdatedModules {
		if !inactive[hubId] && !data.ignoreModuleUpdates[hubId] && count > 0 {
			ch <- prometheus.MustNewConstMetric(hubModulesOutdatedDesc, prometheus.GaugeValue, float64(count), hubId)
		}
	}
	for _, hubId := range data.healthMissing {
		if !inactive[hubId] {
			ch <- prometheus.MustNewConstMetric(hubHealthMissingDesc, prometheus.GaugeValue, 1, hubId)
		}
	}
}

// hubHealthMetricData is what one scrape reads.
type hubHealthMetricData struct {
	reports  []HubHealth
	inactive map[string]bool
	// ignoreModuleUpdates are the hubs that get no outdated-module series.
	ignoreModuleUpdates map[string]bool
	// outdatedModules counts the tracked updates per hub that are pending for longer than the outdated age.
	outdatedModules map[string]int
	// healthMissing are the hubs online for longer than the stale age without a stored report.
	healthMissing []string
}

// getHubHealthMetricData loads the health reports, the inactive and module-update-ignoring hub ids, the outdated module counts and the
// online hubs of one scrape within one timeout. The documents contain only the fields the metrics need.
func (this *Controller) getHubHealthMetricData() (data hubHealthMetricData, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), hubHealthMetricsTimeout)
	defer cancel()
	now := time.Now()
	opts := options.Find().SetProjection(bson.M{"_id": 0, "hub_id": 1, "state": 1, "received": 1})
	cursor, err := this.getHubHealthCollection().Find(ctx, bson.M{}, opts)
	if err != nil {
		return data, err
	}
	if err = cursor.All(ctx, &data.reports); err != nil {
		return data, err
	}
	data.inactive, err = this.getHubInactiveIds(ctx)
	if err != nil {
		return data, err
	}
	data.ignoreModuleUpdates, err = this.getHubIgnoreModuleUpdatesIds(ctx)
	if err != nil {
		return data, err
	}

	cursor, err = this.getHubModuleUpdateInfoCollection().Find(ctx,
		bson.M{"pending_since": bson.M{"$lte": now.Add(-this.hubModuleOutdatedAge).Unix()}},
		options.Find().SetProjection(bson.M{"_id": 0, "hub_id": 1}))
	if err != nil {
		return data, err
	}
	var outdated []HubModuleUpdateInfo
	if err = cursor.All(ctx, &outdated); err != nil {
		return data, err
	}
	data.outdatedModules = map[string]int{}
	for _, info := range outdated {
		data.outdatedModules[info.HubId]++
	}

	if !this.hubHealthConsumerEnabled {
		return data, nil
	}
	cursor, err = this.getHubStateCollection().Find(ctx,
		bson.M{"online": true, "since": bson.M{"$lte": now.Add(-this.hubHealthStaleAge).Unix()}},
		options.Find().SetProjection(bson.M{"_id": 0, "gateway": 1}))
	if err != nil {
		return data, err
	}
	var online []HubState
	if err = cursor.All(ctx, &online); err != nil {
		return data, err
	}
	reported := make(map[string]bool, len(data.reports))
	for _, report := range data.reports {
		reported[report.HubId] = true
	}
	for _, state := range online {
		if !reported[state.Gateway] {
			data.healthMissing = append(data.healthMissing, state.Gateway)
		}
	}
	return data, nil
}
