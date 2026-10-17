package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"

	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/respond"
)

type settingsLimits struct {
	MinCallSpendUSDMicros  int64 `json:"minCallSpendUsdMicros"`
	MaxCallSpendUSDMicros  int64 `json:"maxCallSpendUsdMicros"`
	MaxLowBalanceUSDMicros int64 `json:"maxLowBalanceUsdMicros"`
}

func settingsResponse(settings models.UserSettings) any {
	return struct {
		models.UserSettings
		Limits settingsLimits `json:"limits"`
	}{settings, settingsLimits{
		MinCallSpendUSDMicros:  models.X402ProbeFloorUSDMicros,
		MaxCallSpendUSDMicros:  models.MaxSingleX402QuoteUSDMicros,
		MaxLowBalanceUSDMicros: models.MaxLowBalanceUSDMicros,
	}}
}

func (d *Deps) Settings(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)
	settings, err := d.Store.GetUserSettings(r.Context(), userID)
	if err != nil {
		log.Printf("get settings: %v", err)
		respond.Error(w, http.StatusInternalServerError, "could not load settings")
		return
	}
	respond.JSON(w, http.StatusOK, settingsResponse(settings))
}

func (d *Deps) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	patch, message := parseSettingsPatch(r.Body)
	if message != "" {
		respond.Error(w, http.StatusBadRequest, message)
		return
	}
	userID, _ := r.Context().Value(CtxUserID).(string)
	saved, err := d.Store.UpsertUserSettings(r.Context(), userID, patch)
	if err != nil {
		log.Printf("update settings: %v", err)
		respond.Error(w, http.StatusInternalServerError, "could not save settings")
		return
	}
	respond.JSON(w, http.StatusOK, settingsResponse(saved))
}

func parseSettingsPatch(body io.Reader) (models.UserSettingsPatch, string) {
	var patch models.UserSettingsPatch
	data, err := io.ReadAll(io.LimitReader(body, 4097))
	if err != nil || len(data) > 4096 {
		return patch, "invalid settings body"
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return patch, "settings must be a JSON object"
	}
	for key := range raw {
		if key != "lowBalanceUsdMicros" && key != "maxCallSpendUsdMicros" {
			return patch, "unknown setting: " + key
		}
	}
	if value, present := raw["lowBalanceUsdMicros"]; present {
		var threshold int64
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &threshold) != nil {
			return patch, "lowBalanceUsdMicros must be a whole number of USD micros"
		}
		if threshold < 0 || threshold > models.MaxLowBalanceUSDMicros {
			return patch, "lowBalanceUsdMicros is outside the supported range"
		}
		patch.LowBalanceUSDMicros = &threshold
	}
	if value, present := raw["maxCallSpendUsdMicros"]; present {
		var ceiling *int64
		if err := json.Unmarshal(value, &ceiling); err != nil {
			return patch, "maxCallSpendUsdMicros must be a whole number of USD micros, or null"
		}
		if ceiling != nil && (*ceiling < models.X402ProbeFloorUSDMicros || *ceiling > models.MaxSingleX402QuoteUSDMicros) {
			return patch, "maxCallSpendUsdMicros is outside the supported range"
		}
		patch.SetMaxCallSpend = true
		patch.MaxCallSpendUSDMicros = ceiling
	}
	return patch, ""
}
