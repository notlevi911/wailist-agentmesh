package nodes

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"

	"github.com/agentmesh/backend/internal/models"
)

// elevenLabsAPIBase is overridden in tests via SetElevenLabsAPIBaseForTest.
var elevenLabsAPIBase = "https://api.elevenlabs.io"

// SetElevenLabsAPIBaseForTest overrides the ElevenLabs API base URL. Call
// only from tests. Pass "" to reset to the real API.
func SetElevenLabsAPIBaseForTest(base string) {
	if base == "" {
		elevenLabsAPIBase = "https://api.elevenlabs.io"
	} else {
		elevenLabsAPIBase = base
	}
}

func sendElevenLabs(ctx context.Context, node models.WorkflowNode, rc RunContexter) (any, error) {
	apiKey := secretVal(node, "elevenlabsAPIKey")
	if apiKey == "" {
		return "elevenlabs_skipped_no_api_key", ErrActionSkipped
	}
	voiceID := configVal(node, "elevenlabsVoiceID", "21m00Tcm4TlvDq8ikWAM")
	target := elevenLabsAPIBase + "/v1/text-to-speech/" + url.PathEscape(voiceID)
	payload := map[string]any{"text": resolveMessage(node, rc), "model_id": "eleven_monolingual_v1"}
	headers := map[string]string{"xi-api-key": apiKey}
	req, err := newJSONRequest(ctx, http.MethodPost, target, headers, payload)
	if err != nil {
		return nil, fmt.Errorf("ElevenLabs: %w", err)
	}
	resp, err := doValidatedRequest(req, "ElevenLabs")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("ElevenLabs API %d: %s", resp.StatusCode, readErrorBody(resp))
	}
	audio, err := readBounded(resp.Body, mediaResponseLimit)
	if err != nil {
		return nil, fmt.Errorf("ElevenLabs: read audio: %w", err)
	}
	return map[string]any{
		"status":      "elevenlabs_audio_generated",
		"audioBase64": base64.StdEncoding.EncodeToString(audio),
	}, nil
}
