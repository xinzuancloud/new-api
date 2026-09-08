package model

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/protocol_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProtocolProfilesTransactions(t *testing.T) {
	db := useFrontendOptionMigrationDB(t)
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))
	oldCatalog := protocol_setting.JSON(protocol_setting.Get())
	oldCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	oldOption, hadOption := common.OptionMap[protocol_setting.OptionKey]
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		require.NoError(t, protocol_setting.Update(oldCatalog))
		common.MemoryCacheEnabled = oldCache
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if hadOption {
			common.OptionMap[protocol_setting.OptionKey] = oldOption
		} else {
			delete(common.OptionMap, protocol_setting.OptionKey)
		}
	})
	catalog, err := protocol_setting.Parse(`{"vendor":{"name":"Vendor","defaults":{"entry_formats":["openai"],"endpoints":[{"format":"openai","path":"/v1/chat/completions","verified":true}]}}}`)
	require.NoError(t, err)
	revision, err := UpdateProtocolProfiles(protocol_setting.Revision(protocol_setting.Catalog{}), catalog)
	require.NoError(t, err)
	_, err = UpdateProtocolProfiles("stale", catalog)
	require.ErrorIs(t, err, ErrProtocolConflict)
	channel := Channel{Type: 1, Key: "secret-key", Name: "account", Models: "advertised", ModelMapping: common.GetPointer(`{"advertised":"alias","alias":"wire","wire":""}`), Group: "default", BaseURL: common.GetPointer("https://vendor.example"), Setting: common.GetPointer(`{"unrelated":{"preserved":true},"proxy":"http://user:password@proxy.example","protocol_routing":{"enabled":true,"account_resource":"account-1","quota_scope":"account","defaults":{"entry_formats":["openai"],"endpoints":[{"format":"openai","path":"/v1/chat/completions","verified":true}]}}}`)}
	require.NoError(t, channel.Insert())
	metadata, err := ListProtocolChannels()
	require.NoError(t, err)
	require.Len(t, metadata, 1)
	assert.Equal(t, []string{"wire"}, metadata[0].MappedModels)
	metadataJSON, err := common.Marshal(metadata)
	require.NoError(t, err)
	assert.NotContains(t, string(metadataJSON), "secret-key")
	assert.NotContains(t, string(metadataJSON), "password")
	preview, err := BindProtocolChannels(revision, "vendor", []int{channel.Id}, true, "")
	require.NoError(t, err)
	require.Len(t, preview.Changes, 1)
	_, err = BindProtocolChannels(revision, "vendor", []int{channel.Id}, false, "stale")
	require.ErrorIs(t, err, ErrProtocolConflict)
	_, err = BindProtocolChannels(revision, "vendor", []int{channel.Id}, false, preview.Fingerprint)
	require.NoError(t, err)
	require.NoError(t, db.First(&channel, channel.Id).Error)
	assert.Contains(t, *channel.Setting, `"unrelated":{"preserved":true}`)
	assert.Contains(t, *channel.Setting, "user:password")
	assert.Equal(t, "secret-key", channel.Key)
	settings, err := ProtocolChannelSettings(&channel)
	require.NoError(t, err)
	assert.Equal(t, "vendor", settings.ProtocolRouting.Profile)
	assert.Equal(t, "account-1", settings.ProtocolRouting.AccountResource)
	assert.True(t, settings.ProtocolRouting.Enabled)
	_, err = UpdateProtocolProfiles(revision, protocol_setting.Catalog{})
	require.Error(t, err)
	// A bad normal channel write rolls back without introducing a dangling binding.
	invalid := Channel{Id: channel.Id, Setting: common.GetPointer(`{"protocol_routing":{"enabled":true,"profile":"missing"}}`)}
	require.Error(t, invalid.Update())
	require.NoError(t, db.First(&channel, channel.Id).Error)
	assert.Contains(t, *channel.Setting, "vendor")
	// Callback mutations and related option writes commit or roll back together.
	_, err = MutateProtocolProfiles(revision, func(tx *gorm.DB, c protocol_setting.Catalog) error {
		delete(c, "vendor")
		return tx.Create(&Option{Key: "protocol-test-atomic", Value: "no"}).Error
	})
	require.Error(t, err)
	var row Option
	assert.ErrorIs(t, db.Where(map[string]any{"key": "protocol-test-atomic"}).First(&row).Error, gorm.ErrRecordNotFound)
	preview, err = BindProtocolChannels(revision, "", []int{channel.Id}, true, "")
	require.NoError(t, err)
	assert.Equal(t, preview.Changes[0].Before, preview.Changes[0].After)
	_, err = BindProtocolChannels(revision, "", []int{channel.Id}, false, preview.Fingerprint)
	require.NoError(t, err)
	_, err = UpdateProtocolProfiles(revision, protocol_setting.Catalog{})
	require.NoError(t, err)
	require.Error(t, UpdateOption(protocol_setting.OptionKey, "{}"))
	require.Error(t, UpdateOptionsBulk(map[string]string{"ProtocolProbeReport:secret": "{}"}))
	require.NoError(t, updateOptionMap("ProtocolProbeReport:secret", "{\"secret\":true}"))
	common.OptionMapRWMutex.RLock()
	_, exported := common.OptionMap["ProtocolProbeReport:secret"]
	common.OptionMapRWMutex.RUnlock()
	assert.False(t, exported)
	// Malformed reads must not rewrite stored settings.
	channel.Setting = common.GetPointer("broken")
	require.NoError(t, db.Model(&Channel{}).Where("id = ?", channel.Id).Update("setting", "broken").Error)
	_, err = ProtocolChannelSettings(&channel)
	require.Error(t, err)
	require.NoError(t, db.First(&channel, channel.Id).Error)
	assert.Equal(t, "broken", *channel.Setting)
	assert.False(t, errors.Is(err, ErrProtocolConflict))
	// A previously fetched refresh cannot revert a committed catalog.
	stale := protocol_setting.JSON(protocol_setting.Catalog{})
	next, err := UpdateProtocolProfiles(protocol_setting.Revision(protocol_setting.Catalog{}), catalog)
	require.NoError(t, err, "unrelated malformed legacy settings must not block catalog edits")
	require.NoError(t, updateOptionMap(protocol_setting.OptionKey, stale))
	assert.Equal(t, next, protocol_setting.Revision(protocol_setting.Get()))
	metadata, err = ListProtocolChannels()
	require.NoError(t, err)
	require.Len(t, metadata, 1)
	assert.Equal(t, "invalid_channel_settings", metadata[0].Diagnostic)

}
