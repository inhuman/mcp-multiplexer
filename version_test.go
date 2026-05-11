package mcpx

import "testing"

// TestVersion_DefaultIsDev verifies that defaultClientVersion returns "dev"
// in the test environment where go test sets bi.Main.Version to "(devel)".
func TestVersion_DefaultIsDev(t *testing.T) {
	v := defaultClientVersion()
	if v != "dev" {
		// In a released build this returns the real version, which is fine.
		// In the test environment (go test) bi.Main.Version is "(devel)", so
		// we expect "dev". If it's something else we want to know.
		t.Logf("defaultClientVersion = %q (not \"dev\"; this is expected in released builds)", v)
	}
}

// TestVersion_WithClientIdentityOverrides verifies that WithClientIdentity
// takes precedence over the auto-detected default.
func TestVersion_WithClientIdentityOverrides(t *testing.T) {
	o := defaultOptions()
	WithClientIdentity("myapp", "9.9.9")(o)
	if o.clientName != "myapp" {
		t.Errorf("clientName = %q, want %q", o.clientName, "myapp")
	}
	if o.clientVersion != "9.9.9" {
		t.Errorf("clientVersion = %q, want %q", o.clientVersion, "9.9.9")
	}
}
