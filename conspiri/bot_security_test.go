package conspiribot

import (
	"encoding/json"
	"testing"
)

// TestDefaultInsecureSkipVerify ensures that the default value for InsecureSkipVerify is false (secure)
// when parsing an empty JSON or using default initialization.
func TestDefaultInsecureSkipVerify(t *testing.T) {
	// 1. Test default struct initialization
	config := GlobalServerConfig{}
	if config.InsecureSkipVerify != false {
		t.Errorf("Expected InsecureSkipVerify to be false by default, got %v", config.InsecureSkipVerify)
	}

	// 2. Test unmarshalling from JSON without the field
	jsonConfig := `{"host": "localhost", "port": 6667, "channel": "#test", "use_tls": true}`
	var parsedConfig GlobalServerConfig
	if err := json.Unmarshal([]byte(jsonConfig), &parsedConfig); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	if parsedConfig.InsecureSkipVerify != false {
		t.Errorf("Expected InsecureSkipVerify to be false when missing in JSON, got %v", parsedConfig.InsecureSkipVerify)
	}
}

// TestExplicitInsecureSkipVerify ensures that we can explicitly set it to true if needed.
func TestExplicitInsecureSkipVerify(t *testing.T) {
	jsonConfig := `{"host": "localhost", "port": 6667, "channel": "#test", "use_tls": true, "insecure_skip_verify": true}`
	var parsedConfig GlobalServerConfig
	if err := json.Unmarshal([]byte(jsonConfig), &parsedConfig); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	if parsedConfig.InsecureSkipVerify != true {
		t.Errorf("Expected InsecureSkipVerify to be true when set in JSON, got %v", parsedConfig.InsecureSkipVerify)
	}
}
