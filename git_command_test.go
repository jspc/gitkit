package gitkit

import (
	"testing"
)

func TestParseGitCommand(t *testing.T) {
	tests := map[string]GitCommand{
		"git-upload-pack 'hello.git'":        {"git-upload-pack", "hello", "git-upload-pack 'hello.git'"},
		"git upload-pack 'hello.git'":        {"git upload-pack", "hello", "git upload-pack 'hello.git'"},
		"git-upload-pack '/hello.git'":       {"git-upload-pack", "hello", "git-upload-pack 'hello.git'"},
		"git-upload-pack '/hello/world.git'": {"git-upload-pack", "hello/world", "git-upload-pack 'hello/world.git'"},
		"git-upload-pack 'hello/world.git'":  {"git-upload-pack", "hello/world", "git-upload-pack 'hello/world.git'"},
		"git-receive-pack 'hello.git'":       {"git-receive-pack", "hello", "git-receive-pack 'hello.git'"},
		"git receive-pack 'hello.git'":       {"git receive-pack", "hello", "git receive-pack 'hello.git'"},
		"git-upload-archive 'hello.git'":     {"git-upload-archive", "hello", "git-upload-archive 'hello.git'"},
		"git upload-archive 'hello.git'":     {"git upload-archive", "hello", "git upload-archive 'hello.git'"},
		"git upload-archive 'hello'":         {"git upload-archive", "hello", "git upload-archive 'hello.git'"},
	}

	for name, gc := range tests {
		t.Run(name, func(t *testing.T) {
			cmd, err := ParseGitCommand(name)
			if err != nil {
				t.Fatal(err)
			}

			t.Run("command", func(t *testing.T) {
				expect := gc.Command
				rcvd := cmd.Command

				if expect != rcvd {
					t.Errorf("expected %q, received %q", expect, rcvd)
				}
			})

			t.Run("repo", func(t *testing.T) {
				expect := gc.Repo
				rcvd := cmd.Repo

				if expect != rcvd {
					t.Errorf("expected %q, received %q", expect, rcvd)
				}
			})
		})
	}
}
