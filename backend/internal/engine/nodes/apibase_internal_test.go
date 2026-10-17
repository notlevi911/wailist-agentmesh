package nodes

import (
	"net/http"
	"testing"
)

// Every registered service can be pointed at a test server and put back. A
// connector that forgets to read through apiBase would still pass its own
// tests against the real API's absence, so the registry itself is checked.
func TestSetAPIBaseForTestOverridesAndResets(t *testing.T) {
	const override = "http://127.0.0.1:1"
	for service, def := range apiBaseDefaults {
		setAPIBaseForTest(service, override)
		if got := apiBase(service); got != override {
			t.Fatalf("%s: after override apiBase = %q, want %q", service, got, override)
		}
		setAPIBaseForTest(service, "")
		if got := apiBase(service); got != def {
			t.Fatalf("%s: after reset apiBase = %q, want default %q", service, got, def)
		}
	}
}

// A typo in a wrapper's service name must fail loudly, not silently
// override nothing while the connector keeps calling the real API.
func TestSetAPIBaseForTestPanicsOnUnknownService(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("want a panic for an unregistered service")
		}
	}()
	setAPIBaseForTest("no-such-connector", "http://127.0.0.1:1")
}

// The exported wrappers other packages' tests call still reach the registry.
func TestExportedAPIBaseSettersRouteToRegistry(t *testing.T) {
	SetNotionAPIBaseForTest("http://notion.test")
	defer SetNotionAPIBaseForTest("")
	if got := apiBase("notion"); got != "http://notion.test" {
		t.Fatalf("notion: apiBase = %q after SetNotionAPIBaseForTest", got)
	}

	// Shopify's two connectors keep separate overrides.
	SetShopifyAPIBaseForTest("http://customer.test")
	defer SetShopifyAPIBaseForTest("")
	if got := apiBase("shopify"); got != "" {
		t.Fatalf("order-note shopify base = %q, want untouched by the customer connector's override", got)
	}

	SetGoogleAPIBasesForTest("http://gmail.test", "http://sheets.test", "http://calendar.test")
	defer SetGoogleAPIBasesForTest("", "", "")
	for service, want := range map[string]string{
		"gmail": "http://gmail.test", "sheets": "http://sheets.test",
		"calendar": "http://calendar.test",
	} {
		if got := apiBase(service); got != want {
			t.Fatalf("%s: apiBase = %q, want %q", service, got, want)
		}
	}
}

func TestBasicAuthHeaderMatchesNetHTTP(t *testing.T) {
	// RFC 7617's own example.
	if got := basicAuthHeader("Aladdin", "open sesame")["Authorization"]; got != "Basic QWxhZGRpbjpvcGVuIHNlc2FtZQ==" {
		t.Fatalf("basicAuthHeader(Aladdin, open sesame) = %q", got)
	}
	for _, c := range []struct{ user, pass string }{
		{"user@example.com/token", "p:a:ss"},
		{"", ""},
		{"ünïcode", "密码"},
	} {
		req, _ := http.NewRequest(http.MethodGet, "http://example.test", nil)
		req.SetBasicAuth(c.user, c.pass)
		if got, want := basicAuthHeader(c.user, c.pass)["Authorization"], req.Header.Get("Authorization"); got != want {
			t.Fatalf("basicAuthHeader(%q, %q) = %q, want %q", c.user, c.pass, got, want)
		}
	}
}
