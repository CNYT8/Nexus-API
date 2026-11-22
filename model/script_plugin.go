package model

import (
	"errors"
	"time"
)

// ScriptPlugin stores immutable executable revisions separately from TaskPlugin,
// whose schema and expression remain the authority for declarative billing.
// Disabling a revision blocks new submissions; existing tasks may still load it.
type ScriptPlugin struct {
	ID      int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	Key     string `json:"key" gorm:"column:plugin_key;type:varchar(30);uniqueIndex:idx_script_plugin_revision"`
	Version string `json:"version" gorm:"type:varchar(64);uniqueIndex:idx_script_plugin_revision"`
	Name    string `json:"name" gorm:"type:varchar(128)"`
	// Let each dialect select its large-string type (MySQL TEXT is only 64 KiB).
	Source    string `json:"source,omitempty" gorm:"size:1048576"`
	Meta      string `json:"meta" gorm:"size:1048576"`
	Digest    string `json:"digest" gorm:"type:varchar(64)"`
	Enabled   bool   `json:"enabled"`
	CreatedAt int64  `json:"created_at"`
}

// Bindings select a driver only after normal token/model/channel authorization.
// The revision ID is copied into TaskPrivateData before the task is inserted.
type ScriptPluginBinding struct {
	ID        int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	ChannelID int    `json:"channel_id" gorm:"uniqueIndex:idx_script_plugin_binding"`
	Model     string `json:"model" gorm:"type:varchar(255);uniqueIndex:idx_script_plugin_binding"`
	PluginID  int64  `json:"plugin_id" gorm:"index"`
	UpdatedAt int64  `json:"updated_at"`
}

func CreateScriptPlugin(plugin *ScriptPlugin) error {
	if plugin == nil || plugin.ID != 0 || plugin.Key == "" || plugin.Source == "" || len(plugin.Source) > 1<<20 || len(plugin.Digest) != 64 {
		return errors.New("invalid script plugin revision")
	}
	plugin.CreatedAt = time.Now().Unix()
	return DB.Create(plugin).Error
}
func GetScriptPlugin(id int64) (*ScriptPlugin, error) {
	if id <= 0 {
		return nil, errors.New("invalid script plugin id")
	}
	var plugin ScriptPlugin
	err := DB.First(&plugin, id).Error
	return &plugin, err
}
func ListScriptPlugins(beforeID int64, limit int) ([]ScriptPlugin, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	query := DB.Omit("source").Where("id NOT IN (?)", DB.Model(&ScriptPluginTombstone{}).Select("plugin_id"))
	if beforeID > 0 {
		query = query.Where("id < ?", beforeID)
	}
	var plugins []ScriptPlugin
	err := query.Order("id DESC").Limit(limit).Find(&plugins).Error
	return plugins, err
}
func SetScriptPluginEnabled(id int64, enabled bool) error {
	return setScriptRevisionEnabled(id, enabled)
}
func GetScriptPluginBinding(channelID int, name string) (*ScriptPluginBinding, error) {
	var binding ScriptPluginBinding
	err := DB.Where("channel_id = ? AND model = ?", channelID, name).First(&binding).Error
	return &binding, err
}
func ListScriptPluginBindings(channelID int) ([]ScriptPluginBinding, error) {
	query := DB.Model(&ScriptPluginBinding{})
	if channelID > 0 {
		query = query.Where("channel_id = ?", channelID)
	}
	var bindings []ScriptPluginBinding
	err := query.Order("id DESC").Limit(1000).Find(&bindings).Error
	return bindings, err
}
func PutScriptPluginBinding(binding *ScriptPluginBinding) error {
	if binding == nil || binding.ChannelID <= 0 || binding.Model == "" || len(binding.Model) > 255 || binding.PluginID < 0 {
		return errors.New("invalid script plugin binding")
	}
	if binding.PluginID == 0 {
		return DB.Where("channel_id = ? AND model = ?", binding.ChannelID, binding.Model).Delete(&ScriptPluginBinding{}).Error
	}
	return putScriptRevisionBinding(binding)
}
