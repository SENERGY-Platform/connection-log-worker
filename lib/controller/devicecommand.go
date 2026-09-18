/*
 * Copyright 2020 InfAI (CC SES)
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
	"maps"
	"slices"

	"github.com/SENERGY-Platform/connection-log-worker/lib/model"
)

func (this *Controller) UpdateDevice(command model.DeviceCommand) error {
	if command.Command == "DELETE" {
		err := this.deleteDeviceLog(command.Id)
		if err != nil {
			return err
		}
		return this.deleteDeviceState(command.Id)
	}
	return nil
}

func (this *Controller) UpdateDevices(commands []model.DeviceCommand) error {
	ids := getUniqueStringCondition(commands, func(i model.DeviceCommand) (bool, string) {
		if i.Command == "DELETE" {
			return true, i.Id
		}
		return false, ""
	})
	err := this.deleteDeviceLogs(ids)
	if err != nil {
		return err
	}
	return this.deleteDeviceStates(ids)
}

func getUniqueStringCondition[T any](sl []T, valFunc func(i T) (bool, string)) []string {
	tmp := make(map[string]struct{})
	for _, i := range sl {
		ok, val := valFunc(i)
		if ok {
			tmp[val] = struct{}{}
		}
	}
	return slices.Collect(maps.Keys(tmp))
}
