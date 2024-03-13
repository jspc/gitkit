package gitkit

import (
	"bytes"
	"os"
	"path/filepath"
	"text/template"
)

var (
	DefaultSSHBanner = `Welcome to gitkit {{ .Name }}
Your public key id is {{ .Id }}
`
)

type Config struct {
	KeyDir         string       // Directory for server ssh keys. Only used in SSH strategy.
	Dir            string       // Directory that contains repositories
	GitPath        string       // Path to git binary
	GitUser        string       // User for ssh connections
	AutoCreate     bool         // Automatically create repostories
	AutoHooks      bool         // Automatically setup git hooks
	Hooks          *HookScripts // Scripts for hooks/* directory
	Auth           bool         // Require authentication
	BannerTemplate string       // text/template string to compile when a user tries to login via ssh, such as when verifying keys
}

// HookScripts represents all repository server-size git hooks
type HookScripts struct {
	PreReceive  string
	Update      string
	PostReceive string
}

// Configure hook scripts in the repo base directory
func (c *HookScripts) setupInDir(path string) error {
	basePath := filepath.Join(path, "hooks")
	scripts := map[string]string{
		"pre-receive":  c.PreReceive,
		"update":       c.Update,
		"post-receive": c.PostReceive,
	}

	// Cleanup any existing hooks first
	hookFiles, err := os.ReadDir(basePath)
	if err == nil {
		for _, file := range hookFiles {
			if err := os.Remove(filepath.Join(basePath, file.Name())); err != nil {
				return err
			}
		}
	}

	// Write new hook files
	for name, script := range scripts {
		fullPath := filepath.Join(basePath, name)

		// Dont create hook if there's no script content
		if script == "" {
			continue
		}

		if err := os.WriteFile(fullPath, []byte(script), 0755); err != nil {
			logError("hook-update", err)
			return err
		}
	}

	return nil
}

func (c *Config) KeyPath() string {
	return filepath.Join(c.KeyDir, "gitkit.rsa")
}

func (c *Config) Setup() error {
	if _, err := os.Stat(c.Dir); err != nil {
		if err = os.Mkdir(c.Dir, 0755); err != nil {
			return err
		}
	}

	if c.AutoHooks {
		return c.setupHooks()
	}

	return nil
}

func (c *Config) setupHooks() error {
	files, err := os.ReadDir(c.Dir)
	if err != nil {
		return err
	}

	for _, file := range files {
		if !file.IsDir() {
			continue
		}

		path := filepath.Join(c.Dir, file.Name())

		if err := c.Hooks.setupInDir(path); err != nil {
			return err
		}
	}

	return nil
}

func (c Config) CompileBanner(pk PublicKey) (banner []byte, err error) {
	tmpl := c.BannerTemplate

	if tmpl == "" {
		tmpl = DefaultSSHBanner
	}

	t, err := template.New("").Parse(tmpl)
	if err != nil {
		return
	}

	out := new(bytes.Buffer)

	err = t.Execute(out, pk)
	banner = out.Bytes()

	return
}
