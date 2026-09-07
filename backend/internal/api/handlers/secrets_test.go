package handlers

import (
	"strings"
	"testing"

	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/wallet"
)

const testEncKey = "0123456789abcdef0123456789abcdef"

func TestEncryptField_PlaintextGetsEncrypted(t *testing.T) {
	enc := encryptField("my-api-key", "", testEncKey)
	if !strings.HasPrefix(enc, encPrefix) {
		t.Errorf("want %q prefix, got %q", encPrefix, enc)
	}
	plain, err := wallet.Decrypt(strings.TrimPrefix(enc, encPrefix), testEncKey)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if plain != "my-api-key" {
		t.Errorf("round-trip: want %q got %q", "my-api-key", plain)
	}
}

func TestEncryptField_SentinelPreservesExisting(t *testing.T) {
	existing := encryptField("original-key", "", testEncKey)
	preserved := encryptField(EncSentinel, existing, testEncKey)
	if preserved != existing {
		t.Errorf("sentinel: want existing %q got %q", existing, preserved)
	}
}

func TestEncryptField_EmptyPreservesExisting(t *testing.T) {
	existing := encryptField("original-key", "", testEncKey)
	preserved := encryptField("", existing, testEncKey)
	if preserved != existing {
		t.Errorf("empty val: want existing %q got %q", existing, preserved)
	}
}

func TestEncryptField_ClearSentinelWipesExisting(t *testing.T) {
	existing := encryptField("original-key", "", testEncKey)
	cleared := encryptField(ClearSentinel, existing, testEncKey)
	if cleared != "" {
		t.Errorf("clear sentinel: want empty got %q", cleared)
	}
}

func TestEncryptField_AlreadyEncryptedPassesThrough(t *testing.T) {
	blob := encryptField("key", "", testEncKey)
	again := encryptField(blob, "", testEncKey)
	if again != blob {
		t.Errorf("already-enc: want passthrough got %q", again)
	}
}

func TestEncryptField_EmptyEncryptionKeyIsNoOp(t *testing.T) {
	result := encryptField("my-api-key", "", "")
	if result != "my-api-key" {
		t.Errorf("empty enc key: want passthrough got %q", result)
	}
}

func TestMaskNodes_EncryptedFieldsGetSentinel(t *testing.T) {
	nodes := []models.WorkflowNode{
		{ID: "n1", APIKey: encPrefix + "someciphertext"},
		{ID: "n2", APIKey: "plaintext-not-encrypted"},
		{ID: "n3", EmailAPIKey: encPrefix + "emailcipher"},
		{ID: "n4"},
	}
	masked := maskNodes(nodes)

	if masked[0].APIKey != EncSentinel {
		t.Errorf("n1 apiKey: want %q got %q", EncSentinel, masked[0].APIKey)
	}
	if masked[1].APIKey != "plaintext-not-encrypted" {
		t.Errorf("n2 apiKey should not be masked, got %q", masked[1].APIKey)
	}
	if masked[2].EmailAPIKey != EncSentinel {
		t.Errorf("n3 emailApiKey: want %q got %q", EncSentinel, masked[2].EmailAPIKey)
	}
	if masked[3].APIKey != "" {
		t.Errorf("n4 empty apiKey should stay empty")
	}
}

func TestMaskNodes_DoesNotMutateOriginal(t *testing.T) {
	nodes := []models.WorkflowNode{
		{ID: "n1", APIKey: encPrefix + "cipher"},
	}
	_ = maskNodes(nodes)
	if nodes[0].APIKey != encPrefix+"cipher" {
		t.Error("original slice was mutated")
	}
}

func TestDecryptNodes_DecryptsEncryptedFields(t *testing.T) {
	enc1 := encryptField("gemini-key", "", testEncKey)
	enc2 := encryptField("resend-key", "", testEncKey)
	nodes := []models.WorkflowNode{
		{ID: "n1", APIKey: enc1},
		{ID: "n2", EmailAPIKey: enc2},
		{ID: "n3", APIKey: "plaintext"},
	}
	dec := decryptNodes(nodes, testEncKey)

	if dec[0].APIKey != "gemini-key" {
		t.Errorf("n1 apiKey: want %q got %q", "gemini-key", dec[0].APIKey)
	}
	if dec[1].EmailAPIKey != "resend-key" {
		t.Errorf("n2 emailApiKey: want %q got %q", "resend-key", dec[1].EmailAPIKey)
	}
	if dec[2].APIKey != "plaintext" {
		t.Errorf("n3: plaintext should pass through, got %q", dec[2].APIKey)
	}
}

func TestDecryptNodes_DoesNotMutateOriginal(t *testing.T) {
	enc := encryptField("key", "", testEncKey)
	nodes := []models.WorkflowNode{{ID: "n1", APIKey: enc}}
	_ = decryptNodes(nodes, testEncKey)
	if nodes[0].APIKey != enc {
		t.Error("original slice was mutated")
	}
}

func TestDecryptNodes_EmptyKeyIsNoOp(t *testing.T) {
	enc := encryptField("key", "", testEncKey)
	nodes := []models.WorkflowNode{{ID: "n1", APIKey: enc}}
	dec := decryptNodes(nodes, "")
	if dec[0].APIKey != enc {
		t.Errorf("empty enc key: want passthrough got %q", dec[0].APIKey)
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	original := []models.WorkflowNode{
		{ID: "n1", APIKey: "AIzaSyCU2kNP-test", EmailAPIKey: "re_test123"},
	}
	encrypted := encryptNodes(original, testEncKey, nil)
	if !strings.HasPrefix(encrypted[0].APIKey, encPrefix) {
		t.Error("apiKey should be encrypted")
	}
	if !strings.HasPrefix(encrypted[0].EmailAPIKey, encPrefix) {
		t.Error("emailApiKey should be encrypted")
	}

	masked := maskNodes(encrypted)
	if masked[0].APIKey != EncSentinel {
		t.Errorf("masked apiKey: want %q got %q", EncSentinel, masked[0].APIKey)
	}
	if masked[0].EmailAPIKey != EncSentinel {
		t.Errorf("masked emailApiKey: want %q got %q", EncSentinel, masked[0].EmailAPIKey)
	}

	decrypted := decryptNodes(encrypted, testEncKey)
	if decrypted[0].APIKey != "AIzaSyCU2kNP-test" {
		t.Errorf("decrypted apiKey: want original got %q", decrypted[0].APIKey)
	}
	if decrypted[0].EmailAPIKey != "re_test123" {
		t.Errorf("decrypted emailApiKey: want original got %q", decrypted[0].EmailAPIKey)
	}
}

func TestEncryptNodes_SentinelPreservesExistingEncryptedBlob(t *testing.T) {
	existing := []models.WorkflowNode{
		{ID: "n1", APIKey: encPrefix + "existingciphertext"},
	}
	incoming := []models.WorkflowNode{
		{ID: "n1", APIKey: EncSentinel},
	}
	result := encryptNodes(incoming, testEncKey, existing)
	if result[0].APIKey != encPrefix+"existingciphertext" {
		t.Errorf("sentinel should preserve existing: got %q", result[0].APIKey)
	}
}

func TestEncryptNodes_NewNodeWithSentinelHasNoKey(t *testing.T) {
	// A brand-new node (not in existing) sending sentinel → no key stored
	incoming := []models.WorkflowNode{
		{ID: "new-node", APIKey: EncSentinel},
	}
	result := encryptNodes(incoming, testEncKey, nil)
	if result[0].APIKey != "" {
		t.Errorf("new node with sentinel: want empty got %q", result[0].APIKey)
	}
}

func TestEncryptNodes_SecretsMapGetsEncrypted(t *testing.T) {
	nodes := []models.WorkflowNode{
		{ID: "n1", Secrets: map[string]string{"slackWebhookURL": "https://hooks.slack.com/services/T00/B00/xxx"}},
	}
	encrypted := encryptNodes(nodes, testEncKey, nil)
	if !strings.HasPrefix(encrypted[0].Secrets["slackWebhookURL"], encPrefix) {
		t.Errorf("secrets map value should be encrypted, got %q", encrypted[0].Secrets["slackWebhookURL"])
	}
}

func TestMaskNodes_SecretsMapGetsSentinel(t *testing.T) {
	nodes := []models.WorkflowNode{
		{ID: "n1", Secrets: map[string]string{"slackWebhookURL": encPrefix + "cipher", "plain": "not-encrypted-yet"}},
	}
	masked := maskNodes(nodes)
	if masked[0].Secrets["slackWebhookURL"] != EncSentinel {
		t.Errorf("want sentinel, got %q", masked[0].Secrets["slackWebhookURL"])
	}
	if masked[0].Secrets["plain"] != "not-encrypted-yet" {
		t.Errorf("unencrypted map values should pass through unmasked, got %q", masked[0].Secrets["plain"])
	}
}

func TestDecryptNodes_SecretsMapDecrypts(t *testing.T) {
	enc := encryptField("my-token", "", testEncKey)
	nodes := []models.WorkflowNode{{ID: "n1", Secrets: map[string]string{"githubToken": enc}}}
	dec := decryptNodes(nodes, testEncKey)
	if dec[0].Secrets["githubToken"] != "my-token" {
		t.Errorf("want decrypted value, got %q", dec[0].Secrets["githubToken"])
	}
}

func TestEncryptNodes_SecretsMapSentinelPreservesExisting(t *testing.T) {
	existing := []models.WorkflowNode{
		{ID: "n1", Secrets: map[string]string{"slackWebhookURL": encPrefix + "existingcipher"}},
	}
	incoming := []models.WorkflowNode{
		{ID: "n1", Secrets: map[string]string{"slackWebhookURL": EncSentinel}},
	}
	result := encryptNodes(incoming, testEncKey, existing)
	if result[0].Secrets["slackWebhookURL"] != encPrefix+"existingcipher" {
		t.Errorf("sentinel should preserve existing per-key blob: got %q", result[0].Secrets["slackWebhookURL"])
	}
}

func TestEncryptNodes_NilSecretsMapPreservesExisting(t *testing.T) {
	existing := []models.WorkflowNode{
		{ID: "n1", Secrets: map[string]string{"slackWebhookURL": encPrefix + "existingcipher"}},
	}
	incoming := []models.WorkflowNode{
		{ID: "n1", Template: "slack"},
	}
	result := encryptNodes(incoming, testEncKey, existing)
	if result[0].Secrets["slackWebhookURL"] != encPrefix+"existingcipher" {
		t.Errorf("omitted secrets map should preserve existing map: got %v", result[0].Secrets)
	}
}

func TestEncryptNodes_SecretsMapClearSentinelWipesExistingKey(t *testing.T) {
	existing := []models.WorkflowNode{
		{ID: "n1", Secrets: map[string]string{"httpHeadersJSON": encPrefix + "existingcipher"}},
	}
	incoming := []models.WorkflowNode{
		{ID: "n1", Secrets: map[string]string{"httpHeadersJSON": ClearSentinel}},
	}
	result := encryptNodes(incoming, testEncKey, existing)
	if result[0].Secrets["httpHeadersJSON"] != "" {
		t.Errorf("clear sentinel should wipe the existing per-key blob: got %q", result[0].Secrets["httpHeadersJSON"])
	}
}

func TestEncryptNodes_SecretsMapPreservesOmittedKeys(t *testing.T) {
	existing := []models.WorkflowNode{
		{ID: "n1", Secrets: map[string]string{
			"slackWebhookURL": encPrefix + "existingcipher",
			"notionAPIKey":    encPrefix + "othercipher",
		}},
	}
	incoming := []models.WorkflowNode{
		{ID: "n1", Secrets: map[string]string{"slackWebhookURL": EncSentinel}},
	}
	result := encryptNodes(incoming, testEncKey, existing)
	if result[0].Secrets["notionAPIKey"] != encPrefix+"othercipher" {
		t.Errorf("key omitted from incoming map should be carried forward: got %v", result[0].Secrets)
	}
}

func TestMaskNodes_NilSecretsMapStaysNil(t *testing.T) {
	nodes := []models.WorkflowNode{{ID: "n1"}}
	masked := maskNodes(nodes)
	if masked[0].Secrets != nil {
		t.Errorf("want nil, got %v", masked[0].Secrets)
	}
}

func TestRedactNodesForBuildAgent_StripsFileParamValue(t *testing.T) {
	nodes := []models.WorkflowNode{{
		ID:   "n1",
		Type: models.NodeTypeTool402,
		CustomParams: []models.CustomParam{
			{Name: "resume", Kind: "file", Value: "base64hugefilecontent==", FileName: "resume.pdf", MIMEType: "application/pdf"},
			{Name: "note", Kind: "text", Value: "keep this"},
		},
	}}
	out := redactNodesForBuildAgent(nodes)
	got := out[0].CustomParams
	if got[0].Value != "" {
		t.Errorf("want file bytes stripped, got %q", got[0].Value)
	}
	if got[0].FileName != "resume.pdf" || got[0].MIMEType != "application/pdf" {
		t.Errorf("want file metadata kept so the agent still knows a file is attached, got %+v", got[0])
	}
	if got[1].Value != "keep this" {
		t.Errorf("want a text param untouched, got %q", got[1].Value)
	}
}

func TestRedactNodesForBuildAgent_AlsoMasksSecrets(t *testing.T) {
	nodes := []models.WorkflowNode{{
		ID:     "n1",
		APIKey: encPrefix + "ciphertext",
	}}
	out := redactNodesForBuildAgent(nodes)
	if out[0].APIKey != EncSentinel {
		t.Errorf("want the usual maskNodes sentinel behaviour preserved, got %q", out[0].APIKey)
	}
}

func TestRedactNodesForBuildAgent_DoesNotMutateInput(t *testing.T) {
	nodes := []models.WorkflowNode{{
		ID: "n1",
		CustomParams: []models.CustomParam{
			{Name: "resume", Kind: "file", Value: "base64content"},
		},
	}}
	_ = redactNodesForBuildAgent(nodes)
	if nodes[0].CustomParams[0].Value != "base64content" {
		t.Errorf("want the caller's original slice untouched, got %q", nodes[0].CustomParams[0].Value)
	}
}
