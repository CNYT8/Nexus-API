package service

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/setting"
	"gorm.io/gorm"
)

// A bounded immutable-revision cache prevents source recompilation on each poll.
// Loading is serialized to avoid multiplying runtime memory on this small host.
var scriptPluginCache = struct {
	sync.Mutex
	plugins map[int64]*jsplugin.LoadedPlugin
	order   []int64
}{plugins: map[int64]*jsplugin.LoadedPlugin{}}

func LoadScriptPlugin(ctx context.Context, id int64, forNewTask bool) (*jsplugin.LoadedPlugin, error) {
	// Query the enabled flag on every submission, including cache hits and other
	// application instances; a disabled revision remains available for polling.
	revision, err := model.GetScriptPlugin(id)
	if err != nil {
		return nil, err
	}
	if forNewTask && (setting.ScriptPluginsDisabled() || !revision.Enabled) {
		return nil, errors.New("script plugin is disabled")
	}
	scriptPluginCache.Lock()
	defer scriptPluginCache.Unlock()
	if plugin := scriptPluginCache.plugins[id]; plugin != nil {
		if plugin.Digest != revision.Digest {
			return nil, errors.New("immutable script plugin digest changed")
		}
		return plugin, nil
	}
	plugin, err := jsplugin.CompilePlugin(ctx, revision.Source, jsplugin.Options{Key: revision.Key, Version: revision.Version, Concurrency: 2})
	if err != nil {
		return nil, err
	}
	if plugin.Digest != revision.Digest {
		return nil, errors.New("script plugin digest mismatch")
	}
	if len(scriptPluginCache.order) >= 32 {
		delete(scriptPluginCache.plugins, scriptPluginCache.order[0])
		scriptPluginCache.order = scriptPluginCache.order[1:]
	}
	scriptPluginCache.plugins[id] = plugin
	scriptPluginCache.order = append(scriptPluginCache.order, id)
	return plugin, nil
}

func InstallScriptPlugin(ctx context.Context, source string, enabled bool) (*model.ScriptPlugin, error) {
	plugin, err := jsplugin.CompilePlugin(ctx, source, jsplugin.Options{Concurrency: 2})
	if err != nil {
		return nil, err
	}
	meta, err := common.Marshal(plugin.Meta)
	if err != nil {
		return nil, err
	}
	revision := &model.ScriptPlugin{Key: plugin.Meta.Key, Name: plugin.Meta.Name, Version: plugin.Meta.Version,
		Source: source, Meta: string(meta), Digest: plugin.Digest, Enabled: enabled}
	if err = model.CreateScriptPlugin(revision); err != nil {
		return nil, err
	}
	return revision, nil
}

// ResolveScriptPluginBinding is called only after channel selection. Missing
// bindings retain the existing Go adapters; invalid bindings never fall through.
func ResolveScriptPluginBinding(ctx context.Context, channelID int, name string) (*model.ScriptPluginBinding, *jsplugin.LoadedPlugin, error) {
	binding, err := model.GetScriptPluginBinding(channelID, name)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	plugin, err := LoadScriptPlugin(ctx, binding.PluginID, true)
	return binding, plugin, err
}
func SaveScriptPluginBinding(ctx context.Context, binding *model.ScriptPluginBinding) error {
	if binding == nil || strings.TrimSpace(binding.Model) != binding.Model || strings.ContainsAny(binding.Model, "\r\n\x00") {
		return errors.New("invalid binding model")
	}
	channel, err := model.GetChannelById(binding.ChannelID, true)
	if err != nil {
		return err
	}
	allowedModel := false
	for _, name := range strings.Split(channel.Models, ",") {
		if name == binding.Model {
			allowedModel = true
			break
		}
	}
	if !allowedModel {
		return errors.New("model is not enabled on the channel")
	}
	if binding.PluginID != 0 {
		plugin, err := LoadScriptPlugin(ctx, binding.PluginID, true)
		if err != nil {
			return err
		}
		if !plugin.Meta.SupportsModel(binding.Model) {
			return errors.New("model is not declared by the script plugin")
		}
		if len(plugin.Meta.ChannelTypes) > 0 {
			allowed := false
			for _, channelType := range plugin.Meta.ChannelTypes {
				if channelType == channel.Type {
					allowed = true
					break
				}
			}
			if !allowed {
				return errors.New("channel type is not supported by the script plugin")
			}
		}
	}
	return model.PutScriptPluginBinding(binding)
}
