package discovery

import "testing"

// TestIsAllowedMCPURL_BlocksPrivateTargets: the gate used to check only the scheme, so a discovered
// MCP endpoint naming the metadata service or an RFC1918 address passed it. Discovery output is
// data; data must not become a connection to the instance metadata service.
func TestIsAllowedMCPURL_BlocksPrivateTargets(t *testing.T) {
	blocked := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://127.0.0.1:8080/",
		"http://10.0.0.5/",
		"http://192.168.1.10/",
		"http://[::1]/",
		"ftp://example.com/",
	}
	for _, endpoint := range blocked {
		if isAllowedMCPURL(endpoint) {
			t.Errorf("endpoint %q was allowed; it resolves to a private or non-http target", endpoint)
		}
	}

	// A literal public address, so the assertion does not depend on DNS in the test environment.
	if !isAllowedMCPURL("https://1.1.1.1/") {
		t.Error("a public https endpoint was refused")
	}
}
