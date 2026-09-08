package controller

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/protocol_setting"
	"github.com/gin-gonic/gin"
)

func protocolProfileError(c *gin.Context, err error) {
	status := http.StatusBadRequest
	message := "invalid_protocol_configuration"
	if errors.Is(err, model.ErrProtocolConflict) {
		status = http.StatusConflict
		message = "protocol_revision_conflict"
	}
	c.JSON(status, gin.H{"success": false, "message": message})
}
func decodeProtocolProfileRequest(c *gin.Context, target any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 96*1024)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil || common.Unmarshal(body, target) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid_protocol_request"})
		return false
	}
	return true
}
func GetProtocolProfiles(c *gin.Context) {
	catalog, err := model.ReadProtocolProfiles()
	if err != nil {
		protocolProfileError(c, err)
		return
	}
	channels, err := model.ListProtocolChannels()
	if err != nil {
		protocolProfileError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"revision": protocol_setting.Revision(catalog), "profiles": catalog, "channels": channels}})
}
func PutProtocolProfiles(c *gin.Context) {
	var request struct {
		Revision string                   `json:"revision"`
		Profiles protocol_setting.Catalog `json:"profiles"`
	}
	if !decodeProtocolProfileRequest(c, &request) {
		return
	}
	if request.Profiles == nil {
		protocolProfileError(c, errors.New("missing_catalog"))
		return
	}
	revision, err := model.UpdateProtocolProfiles(request.Revision, request.Profiles)
	if err != nil {
		protocolProfileError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"revision": revision}})
}
func BindProtocolProfiles(c *gin.Context) {
	var request struct {
		Revision    string `json:"revision"`
		Profile     string `json:"profile"`
		ChannelIDs  []int  `json:"channel_ids"`
		Preview     bool   `json:"preview"`
		Fingerprint string `json:"fingerprint"`
	}
	if !decodeProtocolProfileRequest(c, &request) {
		return
	}
	result, err := model.BindProtocolChannels(request.Revision, request.Profile, request.ChannelIDs, request.Preview, request.Fingerprint)
	if err != nil {
		protocolProfileError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}
func GetEffectiveProtocolProfile(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		protocolProfileError(c, errors.New("invalid_channel"))
		return
	}
	catalog, err := model.ReadProtocolProfiles()
	if err != nil {
		protocolProfileError(c, err)
		return
	}
	channel, err := model.GetChannelById(id, false)
	if err != nil {
		protocolProfileError(c, err)
		return
	}
	settings, err := model.EffectiveProtocolSettings(catalog, channel)
	if err != nil {
		protocolProfileError(c, err)
		return
	}
	raw, err := model.ProtocolChannelSettings(channel)
	if err != nil {
		protocolProfileError(c, err)
		return
	}
	profile := ""
	if raw.ProtocolRouting != nil {
		profile = raw.ProtocolRouting.Profile
	}
	provenance := gin.H{"order": []string{"profile.defaults", "profile.models", "channel.defaults", "channel.models"}}
	if p, ok := catalog[profile]; ok {
		provenance["profile_revision"] = protocol_setting.ProfileRevision(p)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"revision": protocol_setting.Revision(catalog), "profile": profile, "settings": settings, "provenance": provenance}})
}
