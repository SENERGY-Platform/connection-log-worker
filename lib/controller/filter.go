package controller

import "github.com/SENERGY-Platform/connection-log-worker/lib/model"

func filterHubLogs(logs []model.HubLog) map[string][]model.HubLog {
	return filterLogs(
		logs,
		func(v model.HubLog) string {
			return v.Id
		},
		func(a, b model.HubLog) bool {
			return a.Connected == b.Connected
		},
	)
}

func filterDeviceLogs(logs []model.DeviceLog) map[string][]model.DeviceLog {
	return filterLogs(
		logs,
		func(v model.DeviceLog) string {
			return v.Id
		},
		func(a, b model.DeviceLog) bool {
			return a.Connected == b.Connected
		},
	)
}

func filterLogs[T any](logs []T, keyFunc func(v T) string, sameState func(a, b T) bool) map[string][]T {
	logsMap := make(map[string][]T)
	for _, log := range logs {
		k := keyFunc(log)
		tmp, ok := logsMap[k]
		if ok && sameState(tmp[len(tmp)-1], log) {
			continue
		}
		logsMap[k] = append(tmp, log)
	}
	return logsMap
}
