package gitkit

import (
	"testing"
)

func TestConfig_CompileBanner(t *testing.T) {
	for _, test := range []struct {
		name        string
		banner      string
		expect      string
		expectError bool
	}{
		{"Empty banner returns default banner", "", `Welcome to gitkit test-user
Your public key id is 0xdeadbeef
`, false},
		{"Explicitly setting default banner returns same", DefaultSSHBanner, `Welcome to gitkit test-user
Your public key id is 0xdeadbeef
`, false},
		{"Custom banner returns accordingly", "Hello {{ .Name }}", "Hello test-user", false},
		{"Dodgy banner returns empty string", "{{ .Foo ", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := Config{
				BannerTemplate: test.banner,
			}

			rcvd, err := c.CompileBanner(PublicKey{Id: "0xdeadbeef", Name: "test-user"})
			if err != nil && !test.expectError {
				t.Errorf("unexpected error: %v", err)
			} else if err == nil && test.expectError {
				t.Error("expected error")
			}

			rcvdStr := string(rcvd)
			if test.expect != rcvdStr {
				t.Errorf("expected\n%s\nreceived\n%s\n", test.expect, rcvdStr)
			}
		})
	}
}
