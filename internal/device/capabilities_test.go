package device

import (
	"strings"
	"testing"
)

func TestCollectIsReadOnlyAndListsSupportedABIs(t *testing.T) {
	capabilities := Collect()
	if !capabilities.ReadOnly {
		t.Fatal("capability snapshot is not marked read-only")
	}
	if len(capabilities.SupportedABIs) != 4 {
		t.Fatalf("supported ABI count = %d, want 4", len(capabilities.SupportedABIs))
	}
	for _, abi := range []string{"arm64-v8a", "armeabi-v7a", "x86_64", "x86"} {
		found := false
		for _, supported := range capabilities.SupportedABIs {
			if supported == abi {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("supported ABI %q missing from %v", abi, capabilities.SupportedABIs)
		}
	}
	for _, forbidden := range []string{"IMEI", "serial number", "MAC address", "location", "contacts", "application data", "cookies", "API keys", "session contents"} {
		found := false
		for _, excluded := range capabilities.ExcludedData {
			if excluded == forbidden {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("privacy exclusion %q missing", forbidden)
		}
	}
	if strings.Contains(capabilities.Prompt(), "test-key") {
		t.Fatal("capability prompt contains a test secret")
	}
}
