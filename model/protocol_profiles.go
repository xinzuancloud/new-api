package model

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/QuantumNous/new-api/setting/model_setting"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/protocol_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrProtocolConflict = errors.New("protocol_revision_conflict")
var protocolCatalogMu sync.Mutex

// LockProtocolProfilesForProbe serializes profile and binding mutations across
// instances. Every writer takes this row before channel or probe report rows.
func LockProtocolProfilesForProbe(tx *gorm.DB) (protocol_setting.Catalog, error) {
	row := Option{Key: protocol_setting.OptionKey, Value: "{}"}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return nil, err
	}
	if err := lockForUpdate(tx).Where(map[string]any{"key": protocol_setting.OptionKey}).First(&row).Error; err != nil {
		return nil, err
	}
	return protocol_setting.Parse(row.Value)
}
func ReadProtocolProfiles() (protocol_setting.Catalog, error) {
	var row Option
	err := DB.Where(map[string]any{"key": protocol_setting.OptionKey}).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return protocol_setting.Catalog{}, nil
	}
	if err != nil {
		return nil, err
	}
	return protocol_setting.Parse(row.Value)
}
func UpdateProtocolProfiles(revision string, catalog protocol_setting.Catalog) (string, error) {
	return MutateProtocolProfiles(revision, func(_ *gorm.DB, current protocol_setting.Catalog) error {
		for id := range current {
			delete(current, id)
		}
		for id, p := range catalog {
			current[id] = p
		}
		return nil
	})
}
func MutateProtocolProfiles(revision string, mutate func(*gorm.DB, protocol_setting.Catalog) error) (string, error) {
	protocolCatalogMu.Lock()
	defer protocolCatalogMu.Unlock()
	var value, next string
	err := DB.Transaction(func(tx *gorm.DB) error {
		catalog, e := LockProtocolProfilesForProbe(tx)
		if e != nil {
			return e
		}
		if revision != protocol_setting.Revision(catalog) {
			return ErrProtocolConflict
		}
		if e = mutate(tx, catalog); e != nil {
			return e
		}
		value = protocol_setting.JSON(catalog)
		if _, e = protocol_setting.Parse(value); e != nil {
			return e
		}
		var channels []Channel
		if e = tx.Select("id", "type", "base_url", "setting").Find(&channels).Error; e != nil {
			return e
		}
		for _, ch := range channels {
			if e = validateProtocolChannel(catalog, &ch); e != nil {
				return fmt.Errorf("bound_channel_policy_invalid: channel %d", ch.Id)
			}
		}
		next = protocol_setting.Revision(catalog)
		return tx.Model(&Option{}).Where(map[string]any{"key": protocol_setting.OptionKey}).Update("value", value).Error
	})
	if err != nil {
		return "", err
	}
	// Publishing stays inside the process mutex so a slower commit cannot replace
	// a newer in-process snapshot. Other instances refresh via ordinary options sync.
	if err = updateOptionMap(protocol_setting.OptionKey, value); err != nil {
		return "", err
	}
	return next, nil
}
func validateProtocolChannel(catalog protocol_setting.Catalog, channel *Channel) error {
	settings, e := ProtocolChannelSettings(channel)
	if e != nil {
		return e
	}
	if settings.ProtocolRouting != nil && settings.ProtocolRouting.Enabled && !common.SupportsProtocolRoutingChannelType(channel.Type) {
		return errors.New("protocol channel authentication adapter unsupported")
	}
	return catalog.ValidateChannel(settings.ProtocolRouting, channel.Type, channel.GetBaseURL())
}

// ProtocolChannelSettings is read-only, including for malformed stored JSON.
func ProtocolChannelSettings(channel *Channel) (dto.ChannelSettings, error) {
	var settings dto.ChannelSettings
	if channel.Setting != nil && *channel.Setting != "" {
		if common.UnmarshalJsonStr(*channel.Setting, &settings) != nil {
			return settings, errors.New("invalid_channel_settings")
		}
	}
	return settings, nil
}
func EffectiveProtocolSettings(catalog protocol_setting.Catalog, channel *Channel) (*dto.ProtocolRoutingSettings, error) {
	settings, e := ProtocolChannelSettings(channel)
	if e != nil {
		return nil, e
	}
	s := settings.ProtocolRouting
	if s == nil {
		return nil, nil
	}
	if e = catalog.ValidateChannel(s, channel.Type, channel.GetBaseURL()); e != nil {
		return nil, e
	}
	if _, e = ProtocolMappedModels(channel); e != nil {
		return nil, e
	}
	result := *s
	result.Profile = ""
	result.Models = map[string]dto.ProtocolModelPolicy{}
	if !s.Enabled && s.Profile == "" && len(s.Defaults.Endpoints) == 0 {
		return &result, nil
	}
	result.Defaults, e = catalog.Resolve(s, "")
	if e != nil {
		return nil, e
	}
	names := map[string]bool{}
	for name := range s.Models {
		names[name] = true
	}
	if p, ok := catalog[s.Profile]; ok {
		for name := range p.Models {
			names[name] = true
		}
	}
	for name := range names {
		p, e := catalog.Resolve(s, name)
		if e != nil {
			return nil, e
		}
		result.Models[name] = p
	}
	return &result, nil
}

type ProtocolChannelMetadata struct {
	ID              int      `json:"id"`
	Name            string   `json:"name"`
	Type            int      `json:"type"`
	BaseURL         string   `json:"base_url"`
	Models          []string `json:"models"`
	MappedModels    []string `json:"mapped_models"`
	Profile         string   `json:"profile"`
	Enabled         bool     `json:"enabled"`
	AccountResource string   `json:"account_resource"`
}

func ListProtocolChannels() ([]ProtocolChannelMetadata, error) {
	var channels []Channel
	if e := DB.Select("id", "name", "type", "base_url", "models", "model_mapping", "setting").Order("id").Find(&channels).Error; e != nil {
		return nil, e
	}
	result := make([]ProtocolChannelMetadata, 0, len(channels))
	for _, ch := range channels {
		settings, e := ProtocolChannelSettings(&ch)
		if e != nil {
			return nil, e
		}
		models := ch.GetModels()
		mapped, e := ProtocolMappedModels(&ch)
		if e != nil {
			return nil, e
		}

		baseURL := ch.GetBaseURL()
		if _, special := constant.ChannelSpecialBases[baseURL]; !special {
			if parsed, e := url.Parse(baseURL); e == nil && parsed.Host != "" && (parsed.Scheme == "https" || parsed.Scheme == "http") {
				parsed.User = nil
				parsed.RawQuery = ""
				parsed.ForceQuery = false
				parsed.Fragment = ""
				baseURL = parsed.String()
			} else {
				baseURL = ""
			}
		}

		m := ProtocolChannelMetadata{ID: ch.Id, Name: ch.Name, Type: ch.Type, BaseURL: baseURL, Models: models, MappedModels: mapped}
		if s := settings.ProtocolRouting; s != nil {
			m.Profile = s.Profile
			m.Enabled = s.Enabled
			m.AccountResource = s.AccountResource
		}
		result = append(result, m)
	}
	return result, nil
}

type ProtocolBindingChange struct {
	ID     int                          `json:"id"`
	Name   string                       `json:"name"`
	Before *dto.ProtocolRoutingSettings `json:"before"`
	After  *dto.ProtocolRoutingSettings `json:"after"`
}
type ProtocolBindingResult struct {
	Revision    string                  `json:"revision"`
	Fingerprint string                  `json:"fingerprint"`
	Changes     []ProtocolBindingChange `json:"changes"`
}

func BindProtocolChannels(revision, profile string, ids []int, preview bool, fingerprint string) (ProtocolBindingResult, error) {
	result := ProtocolBindingResult{Revision: revision, Changes: []ProtocolBindingChange{}}
	if len(ids) == 0 || len(ids) > 256 {
		return result, errors.New("invalid_channel_selection")
	}
	ids = append([]int(nil), ids...)
	sort.Ints(ids)
	for i, id := range ids {
		if id <= 0 || i > 0 && id == ids[i-1] {
			return result, errors.New("invalid_channel_selection")
		}
	}
	protocolCatalogMu.Lock()
	defer protocolCatalogMu.Unlock()
	err := DB.Transaction(func(tx *gorm.DB) error {
		catalog, e := LockProtocolProfilesForProbe(tx)
		if e != nil {
			return e
		}
		if revision != protocol_setting.Revision(catalog) {
			return ErrProtocolConflict
		}
		if profile != "" {
			if _, ok := catalog[profile]; !ok {
				return errors.New("protocol_profile_not_found")
			}
		}
		var channels []Channel
		if e = lockForUpdate(tx).Select("id", "name", "type", "base_url", "models", "model_mapping", "setting").Where("id IN ?", ids).Order("id").Find(&channels).Error; e != nil {
			return e
		}
		if len(channels) != len(ids) {
			return errors.New("channel_not_found")
		}
		// Hash the complete relevant nonsecret stored state plus desired catalog action;
		// opaque fingerprint binds preview even when unrelated setting fields change.
		data, e := common.Marshal(struct {
			Revision string
			Profile  string
			Channels []Channel
		}{revision, profile, channels})
		if e != nil {
			return e
		}
		mac := hmac.New(sha256.New, []byte(common.SessionSecret))
		_, _ = mac.Write(data)
		result.Fingerprint = fmt.Sprintf("%x", mac.Sum(nil))
		if !preview && (fingerprint == "" || fingerprint != result.Fingerprint) {
			return ErrProtocolConflict
		}
		for _, ch := range channels {
			before, e := EffectiveProtocolSettings(catalog, &ch)
			if e != nil {
				return e
			}
			settings, e := ProtocolChannelSettings(&ch)
			if e != nil {
				return e
			}
			next := &dto.ProtocolRoutingSettings{}
			if settings.ProtocolRouting != nil {
				*next = *settings.ProtocolRouting
			}
			if profile == "" {
				next = before
			} else {
				next.Profile = profile
				next.Defaults = dto.ProtocolModelPolicy{}
				next.Models = nil
			}
			// Preserve unknown unrelated settings by editing only the protocol_routing key.
			raw := map[string]any{}
			if ch.Setting != nil && strings.TrimSpace(*ch.Setting) != "" {
				if common.UnmarshalJsonStr(*ch.Setting, &raw) != nil {
					return errors.New("invalid_channel_settings")
				}
			}
			raw["protocol_routing"] = next
			encoded, e := common.Marshal(raw)
			if e != nil {
				return e
			}
			ch.Setting = common.GetPointer(string(encoded))
			after, e := EffectiveProtocolSettings(catalog, &ch)
			if e != nil {
				return e
			}
			result.Changes = append(result.Changes, ProtocolBindingChange{ID: ch.Id, Name: ch.Name, Before: before, After: after})
			if !preview {
				if e = tx.Model(&Channel{}).Where("id = ?", ch.Id).Update("setting", string(encoded)).Error; e != nil {
					return e
				}
			}
		}
		return nil
	})
	if err == nil && !preview {
		InitChannelCache()
	}
	return result, err
}

// Unbound inserts cannot affect catalog references and retain their existing
// database dependencies. New references are validated under the catalog lock.
func protocolCatalogForChannelInsert(tx *gorm.DB, channels []Channel) (protocol_setting.Catalog, error) {
	for _, channel := range channels {
		settings, e := ProtocolChannelSettings(&channel)
		if e != nil {
			return nil, e
		}
		if settings.ProtocolRouting != nil && settings.ProtocolRouting.Profile != "" {
			return LockProtocolProfilesForProbe(tx)
		}
	}
	return protocol_setting.Catalog{}, nil
}

// ProtocolMappedModels uses the same alias chain and host reasoning base rules
// as live routing, without inspecting credentials or modifying channel state.
func ProtocolMappedModels(channel *Channel) ([]string, error) {
	settings, e := ProtocolChannelSettings(channel)
	if e != nil {
		return nil, e
	}
	mapping := map[string]string{}
	if channel.ModelMapping != nil && *channel.ModelMapping != "" {
		if common.UnmarshalJsonStr(*channel.ModelMapping, &mapping) != nil {
			return nil, errors.New("invalid_model_mapping")
		}
	}
	models := channel.GetModels()
	mapped := make([]string, 0, len(models))
	for _, name := range models {
		name, _, e = hostreasoning.ResolveModelMapping(mapping, name)
		if e != nil {
			return nil, e
		}
		if !model_setting.GetGlobalSettings().PassThroughRequestEnabled && !settings.PassThroughBodyEnabled {
			name = hostreasoning.BaseModelName(name)
		}
		mapped = append(mapped, name)
	}
	return mapped, nil
}
