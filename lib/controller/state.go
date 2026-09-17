package controller

import "github.com/SENERGY-Platform/connection-log-worker/lib/model"

func getLastHubStates(logs []model.HubLog) map[string]model.HubLog {
	logMap := make(map[string]model.HubLog)
	for _, log := range logs {
		tmp, ok := logMap[log.Id]
		if ok && log.Time.Before(tmp.Time) {
			continue
		}
		logMap[log.Id] = log
	}
	return logMap
}

func getLastDeviceStates(logs []model.DeviceLog) map[string]model.DeviceLog {
	logMap := make(map[string]model.DeviceLog)
	for _, log := range logs {
		tmp, ok := logMap[log.Id]
		if ok && log.Time.Before(tmp.Time) {
			continue
		}
		logMap[log.Id] = log
	}
	return logMap
}
