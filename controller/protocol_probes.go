package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/protocol_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var protocolProbeCancels sync.Map

type protocolProbePlanRequest struct {
	Profile         string              `json:"profile"`
	ChannelIDs      []int               `json:"channel_ids"`
	Models          []string            `json:"models"`
	Formats         []types.RelayFormat `json:"formats"`
	Checks          []string            `json:"checks"`
	MaxOutputTokens *int                `json:"max_output_tokens,omitempty"`
	AllowHosted     *bool               `json:"allow_hosted,omitempty"`
}

func CreateProtocolProbe(c *gin.Context) {
	var request protocolProbePlanRequest
	if !decodeProtocolProfileRequest(c, &request) {
		return
	}
	report, err := planProtocolProbes(request)
	if err == nil {
		err = model.CreateProtocolProbeReport(report)
	}
	if err != nil {
		protocolProfileError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": report})
}

func planProtocolProbes(request protocolProbePlanRequest) (*model.ProtocolProbeReport, error) {
	catalog, err := model.ReadProtocolProfiles()
	if err != nil {
		return nil, err
	}
	profile, exists := catalog[request.Profile]
	if !exists {
		return nil, errors.New("profile_not_found")
	}
	maxTokens := 256
	if request.MaxOutputTokens != nil {
		maxTokens = *request.MaxOutputTokens
	}
	if maxTokens < 1 || maxTokens > 1024 || len(request.Checks) == 0 || len(request.Checks) > 6 || len(request.ChannelIDs) > 128 || len(request.Models) > 128 || len(request.Formats) > 3 {
		return nil, errors.New("invalid_probe_plan")
	}
	allowedHosted := request.AllowHosted != nil && *request.AllowHosted
	checks := map[string]bool{}
	for _, check := range request.Checks {
		if checks[check] {
			return nil, errors.New("duplicate_probe_check")
		}
		checks[check] = true
		switch check {
		case "text", "stream", "tools", "namespaces", "images":
		case "web_search":
			if !allowedHosted {
				return nil, errors.New("hosted_probe_requires_consent")
			}
		default:
			return nil, errors.New("invalid_probe_check")
		}
	}
	formats := map[types.RelayFormat]bool{}
	for _, format := range request.Formats {
		if !dto.IsProtocolRoutingFormat(format) || formats[format] {
			return nil, errors.New("invalid_probe_format")
		}
		formats[format] = true
	}
	models := map[string]bool{}
	for _, name := range request.Models {
		if name == "" || len(name) > 256 || models[name] {
			return nil, errors.New("invalid_probe_model")
		}
		models[name] = true
	}
	query := model.DB.Order("id")
	if len(request.ChannelIDs) > 0 {
		query = query.Where("id IN ?", request.ChannelIDs)
	} else {
		query = query.Where("status = ?", common.ChannelStatusEnabled)
	}
	var channels []model.Channel
	if err = query.Find(&channels).Error; err != nil {
		return nil, err
	}
	if len(request.ChannelIDs) > 0 && len(channels) != len(request.ChannelIDs) {
		return nil, errors.New("invalid_probe_channels")
	}
	report := &model.ProtocolProbeReport{Profile: request.Profile, ProfileRevision: protocol_setting.ProfileRevision(profile), Fingerprints: map[int]string{}, MaxOutputTokens: maxTokens, AllowHosted: allowedHosted}
	seen := map[string]bool{}
	foundModels := map[string]bool{}
	for _, ch := range channels {
		if len(request.ChannelIDs) == 0 {
			// As in catalog administration, unparseable legacy settings cannot
			// supply a binding. Identifiable references still require full validation.
			var reference struct {
				Routing *struct {
					Profile string `json:"profile"`
				} `json:"protocol_routing"`
			}
			if ch.Setting == nil || common.UnmarshalJsonStr(*ch.Setting, &reference) != nil || reference.Routing == nil || reference.Routing.Profile != request.Profile {
				continue
			}
		}
		settings, err := model.ProtocolChannelSettings(&ch)
		if err != nil {
			return nil, err
		}
		if settings.ProtocolRouting == nil || settings.ProtocolRouting.Profile != request.Profile {
			if len(request.ChannelIDs) > 0 {
				return nil, errors.New("channel_profile_mismatch")
			}
			continue
		}
		if !common.SupportsProtocolRoutingChannelType(ch.Type) || catalog.ValidateChannel(settings.ProtocolRouting, ch.Type, ch.GetBaseURL()) != nil {
			return nil, errors.New("invalid_probe_channel_policy")
		}
		if ch.Status != common.ChannelStatusEnabled {
			return nil, errors.New("probe_channel_disabled")
		}
		selected := false
		mappedModels, err := model.ProtocolMappedModels(&ch)
		if err != nil {
			return nil, errors.New("invalid_probe_model_mapping")
		}
		for modelIndex, sourceName := range ch.GetModels() {
			name := mappedModels[modelIndex]
			if len(models) > 0 && !models[sourceName] && !models[name] {
				continue
			}
			if models[sourceName] {
				foundModels[sourceName] = true
			}
			if models[name] {
				foundModels[name] = true
			}
			policy, err := catalog.Resolve(settings.ProtocolRouting, name)
			if err != nil {
				return nil, err
			}
			for _, endpoint := range policy.Endpoints {
				if len(formats) > 0 && !formats[endpoint.Format] {
					continue
				}
				for _, check := range request.Checks {
					if check == "namespaces" && endpoint.Format != types.RelayFormatOpenAIResponses {
						continue
					}
					key := name + "\n" + string(endpoint.Format) + "\n" + endpoint.Path + "\n" + check
					if len(request.ChannelIDs) > 0 {
						key = fmt.Sprint(ch.Id) + "\n" + key
					}
					if seen[key] {
						continue
					}
					seen[key] = true
					selected = true
					// Features/verification are not evidence; reports need only the selected path.
					endpoint.Features = nil
					endpoint.Verified = false
					endpoint.VerifiedAt = ""
					report.Cases = append(report.Cases, model.ProtocolProbeCase{ID: len(report.Cases) + 1, ChannelID: ch.Id, Model: name, Endpoint: endpoint, Check: check, Result: model.ProtocolProbeResult{Outcome: "pending", Reason: "not_run"}})
					if len(report.Cases) > model.MaxProtocolProbeCases {
						return nil, errors.New("probe_plan_too_large")
					}
				}
			}
		}
		if selected {
			report.Fingerprints[ch.Id] = model.ProtocolProbeFingerprint(&ch)
		}
	}
	if len(report.Cases) == 0 || len(foundModels) != len(models) {
		return nil, errors.New("probe_plan_has_no_matching_cases")
	}
	return report, nil
}

func GetProtocolProbes(c *gin.Context) {
	reports, err := model.ListProtocolProbeReports()
	if err != nil {
		protocolProfileError(c, err)
		return
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].CreatedAt > reports[j].CreatedAt })
	c.JSON(http.StatusOK, gin.H{"success": true, "data": reports})
}
func GetProtocolProbe(c *gin.Context) {
	report, err := model.ReadProtocolProbeReport(nil, c.Param("id"), false)
	if err != nil {
		protocolProfileError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": report})
}

// Compare current configuration without exposing it. All case plans use saved
// mapped models, and any relevant edit requires a new plan before more calls.
func verifyProtocolProbeFresh(tx *gorm.DB, report *model.ProtocolProbeReport, catalog protocol_setting.Catalog) (map[int]*model.Channel, error) {
	profile, ok := catalog[report.Profile]
	if !ok || protocol_setting.ProfileRevision(profile) != report.ProfileRevision {
		return nil, model.ErrProtocolConflict
	}
	ids := make([]int, 0, len(report.Fingerprints))
	for id := range report.Fingerprints {
		ids = append(ids, id)
	}
	var channels []model.Channel
	if err := tx.Where("id IN ?", ids).Find(&channels).Error; err != nil {
		return nil, err
	}
	if len(channels) != len(ids) {
		return nil, model.ErrProtocolConflict
	}
	selected := map[int]*model.Channel{}
	for i := range channels {
		ch := &channels[i]
		if ch.Status != common.ChannelStatusEnabled || model.ProtocolProbeFingerprint(ch) != report.Fingerprints[ch.Id] {
			return nil, model.ErrProtocolConflict
		}
		selected[ch.Id] = ch
	}
	return selected, nil
}

func RunProtocolProbe(c *gin.Context) {
	id := c.Param("id")
	current, err := model.ReadProtocolProbeReport(nil, id, false)
	if err != nil {
		protocolProfileError(c, err)
		return
	}
	catalog, err := model.ReadProtocolProfiles()
	if err != nil {
		protocolProfileError(c, err)
		return
	}
	channels, err := verifyProtocolProbeFresh(model.DB, current, catalog)
	if err != nil {
		protocolProfileError(c, err)
		return
	}
	runID := common.GetUUID()
	var batch []model.ProtocolProbeCase
	report, err := model.MutateProtocolProbeReport(id, func(report *model.ProtocolProbeReport) error {
		if report.Status == "cancelled" || report.Status == "applied" {
			return model.ErrProtocolConflict
		}
		if report.LeaseUntil > time.Now().Unix() {
			return model.ErrProtocolConflict
		}
		report.RecoverExpiredLease()
		for i := range report.Cases {
			test := &report.Cases[i]
			if test.Result.Outcome != "pending" {
				continue
			}
			test.Result = model.ProtocolProbeResult{Outcome: "running", Reason: "in_progress"}
			batch = append(batch, *test)
			if len(batch) == 2 {
				break
			}
		}
		if len(batch) == 0 {
			report.Status = "completed"
			report.RunID = ""
			report.LeaseUntil = 0
			return nil
		}
		report.Status = "running"
		report.RunID = runID
		report.LeaseUntil = time.Now().Add(60 * time.Second).Unix()
		return nil
	})
	if err != nil {
		protocolProfileError(c, err)
		return
	}
	if len(batch) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": report})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 55*time.Second)
	protocolProbeCancels.Store(id, cancel)
	defer func() { cancel(); protocolProbeCancels.Delete(id) }()
	// Cancellation persisted between claiming and registering must also win.
	if latest, e := model.ReadProtocolProbeReport(nil, id, false); e != nil || latest.Status == "cancelled" {
		cancel()
	}
	results := make([]model.ProtocolProbeResult, len(batch))
	var wg sync.WaitGroup
	for i, test := range batch {
		wg.Add(1)
		go func(i int, test model.ProtocolProbeCase) {
			defer wg.Done()
			results[i] = relay.RunProtocolProbe(ctx, channels[test.ChannelID], test, report.MaxOutputTokens)
		}(i, test)
	}
	wg.Wait()
	report, err = model.MutateProtocolProbeReport(id, func(report *model.ProtocolProbeReport) error {
		if report.RunID != runID {
			return model.ErrProtocolConflict
		}
		for i, test := range batch {
			report.Cases[test.ID-1].Result = results[i]
		}
		report.RunID = ""
		report.LeaseUntil = 0
		if report.Status != "cancelled" {
			report.Status = "completed"
			for _, test := range report.Cases {
				if test.Result.Outcome == "pending" {
					report.Status = "pending"
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		protocolProfileError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": report})
}

func CancelProtocolProbe(c *gin.Context) {
	report, err := model.MutateProtocolProbeReport(c.Param("id"), func(report *model.ProtocolProbeReport) error {
		if report.Status == "applied" {
			return model.ErrProtocolConflict
		}
		report.RecoverExpiredLease()
		report.Status = "cancelled"
		for i := range report.Cases {
			if report.Cases[i].Result.Outcome == "pending" {
				report.Cases[i].Result = model.ProtocolProbeResult{Outcome: "cancelled", Reason: "cancelled"}
			}
		}
		return nil
	})
	if cancel, ok := protocolProbeCancels.Load(c.Param("id")); ok {
		cancel.(context.CancelFunc)()
	}
	if err != nil {
		protocolProfileError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": report})
}

// Only add observed passing features, at model scope. A channel-only path cannot
// be promoted into a product template. Conflicting account evidence remains
// explicit in the report and does not remove an existing supported feature.
func protocolProbeSuggestion(report *model.ProtocolProbeReport, profile protocol_setting.Profile) (protocol_setting.Profile, error) {
	copy, err := common.DeepCopy(&profile)
	if err != nil {
		return profile, err
	}
	profile = *copy
	if profile.Models == nil {
		profile.Models = map[string]dto.ProtocolModelPolicy{}
	}
	for _, test := range report.Cases {
		if test.Result.Outcome != "passed" {
			continue
		}
		policy, err := profile.Defaults.Merge(profile.Models[test.Model])
		if err != nil {
			return profile, err
		}
		index := -1
		for i, endpoint := range policy.Endpoints {
			if endpoint.Format == test.Endpoint.Format && endpoint.Path == test.Endpoint.Path {
				index = i
				break
			}
		}
		if index < 0 {
			continue
		}
		endpoint := policy.Endpoints[index]
		features := map[string]bool{}
		for _, feature := range test.Result.Features {
			found := false
			for _, existing := range endpoint.Features {
				if existing == feature {
					found = true
					break
				}
			}
			if !found {
				features[feature] = true
			}
		}
		if len(features) == 0 && endpoint.Verified {
			continue
		}
		override := profile.Models[test.Model]
		at := -1
		for i, delta := range override.EndpointOverrides {
			if delta.Format == endpoint.Format && (delta.Path == endpoint.Path || (delta.Path == "" && countProtocolFormat(policy, endpoint.Format) == 1)) {
				at = i
				break
			}
		}
		if at < 0 {
			override.EndpointOverrides = append(override.EndpointOverrides, dto.ProtocolEndpointOverride{Format: endpoint.Format, Path: endpoint.Path})
			at = len(override.EndpointOverrides) - 1
		}
		delta := &override.EndpointOverrides[at]
		if delta.Features == nil {
			delta.Features = map[string]bool{}
		}
		for feature := range features {
			delta.Features[feature] = true
		}
		verified := true
		when := time.Unix(report.UpdatedAt, 0).UTC().Format(time.RFC3339)
		delta.Verified = &verified
		delta.VerifiedAt = &when
		profile.Models[test.Model] = override
	}
	return profile, nil
}
func countProtocolFormat(policy dto.ProtocolModelPolicy, format types.RelayFormat) int {
	n := 0
	for _, endpoint := range policy.Endpoints {
		if endpoint.Format == format {
			n++
		}
	}
	return n
}

func ApplyProtocolProbe(c *gin.Context) {
	var request struct {
		Revision    string `json:"revision"`
		Preview     bool   `json:"preview"`
		Fingerprint string `json:"fingerprint"`
	}
	if !decodeProtocolProfileRequest(c, &request) {
		return
	}
	report, err := model.ReadProtocolProbeReport(nil, c.Param("id"), false)
	if err != nil {
		protocolProfileError(c, err)
		return
	}
	if report.RunID != "" && report.LeaseUntil <= time.Now().Unix() {
		report, err = model.MutateProtocolProbeReport(report.ID, func(saved *model.ProtocolProbeReport) error { saved.RecoverExpiredLease(); return nil })
		if err != nil {
			protocolProfileError(c, err)
			return
		}
	}
	if report.AppliedRevision != "" {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"revision": report.AppliedRevision, "applied": true}})
		return
	}
	catalog, err := model.ReadProtocolProfiles()
	if err != nil {
		protocolProfileError(c, err)
		return
	}
	if request.Revision != protocol_setting.Revision(catalog) {
		protocolProfileError(c, model.ErrProtocolConflict)
		return
	}
	if _, err = verifyProtocolProbeFresh(model.DB, report, catalog); err != nil {
		protocolProfileError(c, err)
		return
	}
	if report.RunID != "" || (report.Status != "completed" && report.Status != "cancelled") {
		protocolProfileError(c, model.ErrProtocolConflict)
		return
	}
	before := catalog[report.Profile]
	after, err := protocolProbeSuggestion(report, before)
	if err != nil {
		protocolProfileError(c, err)
		return
	}
	fingerprint := protocol_setting.ProfileRevision(after)
	if request.Preview {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"revision": request.Revision, "fingerprint": fingerprint, "before": before, "after": after}})
		return
	}
	if request.Fingerprint != fingerprint {
		protocolProfileError(c, model.ErrProtocolConflict)
		return
	}
	revision, err := model.MutateProtocolProfiles(request.Revision, func(tx *gorm.DB, current protocol_setting.Catalog) error {
		saved, err := model.ReadProtocolProbeReport(tx, report.ID, true)
		if err != nil {
			return err
		}
		if saved.AppliedRevision != "" {
			return model.ErrProtocolConflict
		}
		if saved.RunID != "" || (saved.Status != "completed" && saved.Status != "cancelled") {
			return model.ErrProtocolConflict
		}
		if _, err = verifyProtocolProbeFresh(tx, saved, current); err != nil {
			return err
		}
		suggested, err := protocolProbeSuggestion(saved, current[saved.Profile])
		if err != nil {
			return err
		}
		if protocol_setting.ProfileRevision(suggested) != request.Fingerprint {
			return model.ErrProtocolConflict
		}
		current[saved.Profile] = suggested
		saved.AppliedRevision = protocol_setting.Revision(current)
		saved.Status = "applied"
		saved.UpdatedAt = time.Now().Unix()
		return model.SaveProtocolProbeReport(tx, saved)
	})
	if err != nil {
		protocolProfileError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"revision": revision, "applied": true}})
}
