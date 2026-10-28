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
	"testing"

	"github.com/SENERGY-Platform/models/go/models"
)

func TestIsInactive(t *testing.T) {
	attr := func(key string, value string) []models.Attribute {
		return []models.Attribute{{Key: "other", Value: "x"}, {Key: key, Value: value, Origin: "web-ui"}}
	}
	for name, c := range map[string]struct {
		attributes []models.Attribute
		expected   bool
	}{
		"no attributes":         {nil, false},
		"true":                  {attr("inactive", "true"), true},
		"upper case and spaces": {attr("inactive", " True "), true},
		"false":                 {attr("inactive", "false"), false},
		"empty value":           {attr("inactive", ""), false},
		"other value":           {attr("inactive", "yes"), false},
		"other key":             {attr("disabled", "true"), false},
		"key is case sensitive": {attr("Inactive", "true"), false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := isInactive(c.attributes); got != c.expected {
				t.Errorf("expected %v, got %v", c.expected, got)
			}
		})
	}
}
