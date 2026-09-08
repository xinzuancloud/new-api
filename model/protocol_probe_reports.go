package model

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const ProtocolProbePrefix = "ProtocolProbeReport:"
const MaxProtocolProbeCases = 128
const MaxProtocolProbeBytes = 60 * 1024

// Reports retain canonical evidence only. Upstream bodies and credentials never
// enter persistent options or the admin response.
type ProtocolProbeResult struct {
	Outcome      string   `json:"outcome"`
	Reason       string   `json:"reason"`
	HTTPStatus   int      `json:"http_status,omitempty"`
	Terminal     bool     `json:"terminal"`
	ElapsedMS    int64    `json:"elapsed_ms,omitempty"`
	InputTokens  int64    `json:"input_tokens,omitempty"`
	OutputTokens int64    `json:"output_tokens,omitempty"`
	Features     []string `json:"features,omitempty"`
}
type ProtocolProbeCase struct {
	ID        int                  `json:"id"`
	ChannelID int                  `json:"channel_id"`
	Model     string               `json:"model"`
	Endpoint  dto.ProtocolEndpoint `json:"endpoint"`
	Check     string               `json:"check"`
	Result    ProtocolProbeResult  `json:"result"`
}
type ProtocolProbeReport struct {
	ID              string              `json:"id"`
	Profile         string              `json:"profile"`
	ProfileRevision string              `json:"profile_revision"`
	Fingerprints    map[int]string      `json:"fingerprints"`
	CreatedAt       int64               `json:"created_at"`
	UpdatedAt       int64               `json:"updated_at"`
	MaxOutputTokens int                 `json:"max_output_tokens"`
	AllowHosted     bool                `json:"allow_hosted"`
	Status          string              `json:"status"`
	Cases           []ProtocolProbeCase `json:"cases"`
	RunID           string              `json:"-"`
	LeaseUntil      int64               `json:"-"`
	AppliedRevision string              `json:"applied_revision,omitempty"`
}

// Storage fields are separate so a report response never advertises a lease token.
type protocolProbeStored struct {
	ProtocolProbeReport
	RunID      string `json:"run_id,omitempty"`
	LeaseUntil int64  `json:"lease_until,omitempty"`
}

func ProtocolProbeFingerprint(channel *Channel) string {
	// HMAC prevents the fingerprint becoming an offline guess oracle for keys.
	data, _ := common.Marshal(struct {
		ID                                              int
		Type                                            int
		Key                                             string
		BaseURL                                         *string
		Models                                          string
		Mapping, Setting, Params, Headers, Organization *string
		Other, OtherSettings                            string
		MultiKey                                        bool
	}{channel.Id, channel.Type, channel.Key, channel.BaseURL, channel.Models,
		channel.ModelMapping, channel.Setting, channel.ParamOverride, channel.HeaderOverride,
		channel.OpenAIOrganization, channel.Other, channel.OtherSettings, channel.ChannelInfo.IsMultiKey})
	mac := hmac.New(sha256.New, []byte(common.SessionSecret))
	_, _ = mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

func ReadProtocolProbeReport(tx *gorm.DB, id string, locked bool) (*ProtocolProbeReport, error) {
	if len(id) != 32 || strings.IndexFunc(id, func(r rune) bool { return !strings.ContainsRune("0123456789abcdef", r) }) >= 0 {
		return nil, fmt.Errorf("invalid probe report identifier")
	}
	if tx == nil {
		tx = DB
	}
	if locked {
		tx = lockForUpdate(tx)
	}
	var option Option
	if err := tx.Where(map[string]any{"key": ProtocolProbePrefix + id}).First(&option).Error; err != nil {
		return nil, err
	}
	var stored protocolProbeStored
	if err := common.UnmarshalJsonStr(option.Value, &stored); err != nil {
		return nil, fmt.Errorf("invalid stored probe report")
	}
	stored.ProtocolProbeReport.RunID = stored.RunID
	stored.ProtocolProbeReport.LeaseUntil = stored.LeaseUntil
	return &stored.ProtocolProbeReport, nil
}

func SaveProtocolProbeReport(tx *gorm.DB, report *ProtocolProbeReport) error {
	data, err := common.Marshal(protocolProbeStored{*report, report.RunID, report.LeaseUntil})
	if err != nil {
		return err
	}
	if len(data) > MaxProtocolProbeBytes {
		return fmt.Errorf("probe report exceeds 60 KiB")
	}
	return tx.Model(&Option{}).Where(map[string]any{"key": ProtocolProbePrefix + report.ID}).Update("value", string(data)).Error
}

func CreateProtocolProbeReport(report *ProtocolProbeReport) error {
	report.ID = strings.ReplaceAll(common.GetUUID(), "-", "")
	report.CreatedAt = time.Now().Unix()
	report.UpdatedAt = report.CreatedAt
	report.Status = "pending"
	data, err := common.Marshal(protocolProbeStored{ProtocolProbeReport: *report})
	if err != nil {
		return err
	}
	if len(report.Cases) == 0 || len(report.Cases) > MaxProtocolProbeCases || len(data) > MaxProtocolProbeBytes {
		return fmt.Errorf("probe plan exceeds report limits")
	}
	// Catalog serialization gives deterministic retention across concurrent creators.
	return DB.Transaction(func(tx *gorm.DB) error {
		if _, err := LockProtocolProfilesForProbe(tx); err != nil {
			return err
		}
		if err := tx.Create(&Option{Key: ProtocolProbePrefix + report.ID, Value: string(data)}).Error; err != nil {
			return err
		}
		var rows []Option
		if err := tx.Where(clause.Like{Column: clause.Column{Name: "key"}, Value: ProtocolProbePrefix + "%"}).Find(&rows).Error; err != nil {
			return err
		}
		type oldReport struct {
			key     string
			created int64
		}
		candidates := make([]oldReport, 0, len(rows))
		for _, row := range rows {
			var old protocolProbeStored
			if common.UnmarshalJsonStr(row.Value, &old) == nil && old.ID != report.ID && old.LeaseUntil < time.Now().Unix() {
				candidates = append(candidates, oldReport{row.Key, old.CreatedAt})
			}
		}
		// A small bounded list; select oldest without exposing report JSON to SQL.
		for len(rows) > 20 && len(candidates) > 0 {
			oldest := 0
			for i := range candidates {
				if candidates[i].created < candidates[oldest].created {
					oldest = i
				}
			}
			if err := tx.Where(map[string]any{"key": candidates[oldest].key}).Delete(&Option{}).Error; err != nil {
				return err
			}
			candidates = append(candidates[:oldest], candidates[oldest+1:]...)
			rows = rows[:len(rows)-1]
		}
		if len(rows) > 20 {
			return fmt.Errorf("too many active probe reports")
		}
		return nil
	})
}

// Mutations acquire a row lock and save within the same transaction. A late
// result cannot overwrite cancellation or another batch's lease.
func MutateProtocolProbeReport(id string, mutate func(*ProtocolProbeReport) error) (*ProtocolProbeReport, error) {
	var report *ProtocolProbeReport
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		report, err = ReadProtocolProbeReport(tx, id, true)
		if err != nil {
			return err
		}
		if err = mutate(report); err != nil {
			return err
		}
		report.UpdatedAt = time.Now().Unix()
		return SaveProtocolProbeReport(tx, report)
	})
	return report, err
}

func ListProtocolProbeReports() ([]ProtocolProbeReport, error) {
	var rows []Option
	if err := DB.Where(clause.Like{Column: clause.Column{Name: "key"}, Value: ProtocolProbePrefix + "%"}).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]ProtocolProbeReport, 0, len(rows))
	for _, row := range rows {
		var stored protocolProbeStored
		if err := common.UnmarshalJsonStr(row.Value, &stored); err != nil {
			return nil, errors.New("invalid stored probe report")
		}
		result = append(result, stored.ProtocolProbeReport)
	}
	return result, nil
}
