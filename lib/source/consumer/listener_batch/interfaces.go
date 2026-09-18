package listener_batch

import "github.com/SENERGY-Platform/connection-log-worker/lib/model"

type Controller interface {
	LogHubs(logs []model.HubLog) error
	LogDevices(logs []model.DeviceLog) error
	UpdateHubs(commands []model.HubCommand) error
	UpdateDevices(commands []model.DeviceCommand) error
}
