/*
Copyright (C) 2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/

package ticket_setting

import (
	"github.com/QuantumNous/new-api/setting/config"
)

const (
	DefaultMaxContentLength = 4000
	MinMaxContentLength     = 100
	MaxMaxContentLength     = 20000
)

type Settings struct {
	Enabled            bool `json:"enabled"`
	AdminManageEnabled bool `json:"admin_manage_enabled"`
	AdminCanClose      bool `json:"admin_can_close"`
	MaxContentLength   int  `json:"max_content_length"`
}

var settings = Settings{
	Enabled:            true,
	AdminManageEnabled: true,
	AdminCanClose:      true,
	MaxContentLength:   DefaultMaxContentLength,
}

func init() {
	config.GlobalConfig.Register("ticket_setting", &settings)
}

func GetSettings() Settings {
	value := settings
	if value.MaxContentLength < MinMaxContentLength || value.MaxContentLength > MaxMaxContentLength {
		value.MaxContentLength = DefaultMaxContentLength
	}
	return value
}

func IsEnabled() bool {
	return GetSettings().Enabled
}

func IsAdminManageEnabled() bool {
	return GetSettings().AdminManageEnabled
}
