package gitkit

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

var (
	ErrAlreadyStarted = errors.New("server has already been started")
	ErrNoListener     = errors.New("cannot call Serve() before Listen()")
	ErrIncorrectUser  = errors.New("unrecognised/ invalid user")
)

type PublicKey struct {
	Id          string
	Name        string
	Fingerprint string
	Content     string
}

type PublicKeyContextKey struct{}
type UserContextKey struct{}

const (
	keyID   = "key-id"
	keyName = "key-name"
	sshUser = "ssh-user"
)

type SSH struct {
	listener net.Listener

	sshconfig *ssh.ServerConfig
	config    *Config

	PublicKeyLookupFunc    func(ctx context.Context, publicKeyPayload string) (*PublicKey, error)
	PreLoginFunc           func(ctx context.Context, metadata ssh.ConnMetadata) error
	AuthoriseOperationFunc func(ctx context.Context, cmd *GitCommand) error
}

func NewSSH(config Config) *SSH {
	s := &SSH{config: &config}

	// Use PATH if full path is not specified
	if s.config.GitPath == "" {
		s.config.GitPath = "git"
	}
	return s
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || os.IsExist(err)
}

func cleanCommand(cmd string) string {
	i := strings.Index(cmd, "git")
	if i == -1 {
		return cmd
	}
	return cmd[i:]
}

func execCommandBytes(cmdname string, args ...string) ([]byte, []byte, error) {
	bufOut := new(bytes.Buffer)
	bufErr := new(bytes.Buffer)

	cmd := exec.Command(cmdname, args...)
	cmd.Stdout = bufOut
	cmd.Stderr = bufErr

	err := cmd.Run()
	return bufOut.Bytes(), bufErr.Bytes(), err
}

func execCommand(cmdname string, args ...string) (string, string, error) {
	bufOut, bufErr, err := execCommandBytes(cmdname, args...)
	return string(bufOut), string(bufErr), err
}

func (s *SSH) handleConnection(ctx context.Context, chans <-chan ssh.NewChannel) {
	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		ch, reqs, err := newChan.Accept()
		if err != nil {
			log.Printf("error accepting channel: %v", err)
			continue
		}

		go func(in <-chan *ssh.Request) {
			defer ch.Close()

			for req := range in {
				s.handleRequest(ctx, ch, req)
			}

		}(reqs)
	}
}

func (s SSH) handleRequest(ctx context.Context, ch ssh.Channel, req *ssh.Request) {
	payload := cleanCommand(string(req.Payload))

	switch req.Type {
	case "env":
		log.Printf("ssh: incoming env request: %s\n", payload)

		err := s.handleEnvRequest(payload)
		if err != nil {
			log.Print(err)
		}

	case "exec":
		log.Printf("ssh: incoming exec request: %s\n", payload)

		err := s.handleExecRequest(ctx, ch, req, payload)
		if err != nil {
			log.Print(err)
		}

		ch.Close()

	case "shell":
		pk := ctx.Value(PublicKeyContextKey{}).(PublicKey)

		banner, err := s.config.CompileBanner(pk)
		if err != nil {
			log.Print(err)
		}

		ch.Write(banner)
		ch.Close()

	default:
		log.Printf("ssh: ignoring %s request", req.Type)
	}
}

func (s SSH) handleEnvRequest(payload string) error {
	args := strings.Split(strings.Replace(payload, "\x00", "", -1), "\v")
	if len(args) != 2 {
		return fmt.Errorf("env: invalid env arguments: '%#v'", args)
	}

	args[0] = strings.TrimLeft(args[0], "\x04")
	if len(args[0]) == 0 {
		return fmt.Errorf("env: invalid key from payload: %s", payload)
	}

	_, _, err := execCommandBytes("env", args[0]+"="+args[1])
	if err != nil {
		log.Printf("env: %v", err)
	}

	return err
}

func (s SSH) handleExecRequest(ctx context.Context, ch ssh.Channel, req *ssh.Request, payload string) (err error) {
	cmdName := strings.TrimLeft(payload, "'()")
	log.Printf("ssh: payload '%v'", cmdName)

	if strings.HasPrefix(cmdName, "\x00") {
		cmdName = strings.Replace(cmdName, "\x00", "", -1)[1:]
	}

	gitcmd, err := ParseGitCommand(cmdName)
	if err != nil {
		ch.Write([]byte("Invalid command.\r\n"))

		return err
	}

	if s.AuthoriseOperationFunc != nil {
		err = s.AuthoriseOperationFunc(ctx, gitcmd)
		if err != nil {
			return
		}
	}

	if !repoExists(filepath.Join(s.config.Dir, gitcmd.Repo)) && s.config.AutoCreate == true {
		err = initRepo(gitcmd.Repo, s.config)
		if err != nil {
			return
		}
	}

	keyID := ctx.Value(PublicKeyContextKey{}).(PublicKey).Id

	cmd := exec.Command(gitcmd.Command, gitcmd.Repo)
	cmd.Dir = s.config.Dir
	cmd.Env = append(os.Environ(), "GITKIT_KEY="+keyID)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("ssh: cant open stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("ssh: cant open stderr pipe: %w", err)
	}

	input, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("ssh: cant open stdin pipe: %w", err)
	}

	if err = cmd.Start(); err != nil {
		return fmt.Errorf("ssh: start error: %w", err)
	}

	req.Reply(true, nil)

	go io.Copy(input, ch)
	io.Copy(ch, stdout)
	io.Copy(ch.Stderr(), stderr)

	if err = cmd.Wait(); err != nil {
		return fmt.Errorf("ssh: command failed: %w", err)
	}

	_, err = ch.SendRequest("exit-status", true, []byte{0, 0, 0, 0})

	return
}

func (s *SSH) createServerKey() error {
	if err := os.MkdirAll(s.config.KeyDir, os.ModePerm); err != nil {
		return err
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}

	privateKeyFile, err := os.Create(s.config.KeyPath())
	if err != nil {
		return err
	}

	if err := os.Chmod(s.config.KeyPath(), 0600); err != nil {
		return err
	}
	defer privateKeyFile.Close()
	if err != nil {
		return err
	}
	privateKeyPEM := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)}
	if err := pem.Encode(privateKeyFile, privateKeyPEM); err != nil {
		return err
	}

	pubKeyPath := s.config.KeyPath() + ".pub"
	pub, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(pubKeyPath, ssh.MarshalAuthorizedKey(pub), 0644)
}

func (s SSH) defaultPreLoginFunc(ctx context.Context, metadata ssh.ConnMetadata) error {
	u := metadata.User()
	ctx = context.WithValue(ctx, UserContextKey{}, u)

	if s.config.Auth && s.config.GitUser != "" && u != s.config.GitUser {
		return ErrIncorrectUser
	}

	return nil
}

func (s *SSH) setup() error {
	if s.sshconfig != nil {
		return nil
	}

	config := &ssh.ServerConfig{
		ServerVersion: fmt.Sprintf("SSH-2.0-gitkit %s", Version),
	}

	if s.config.KeyDir == "" {
		return fmt.Errorf("key directory is not provided")
	}

	if !s.config.Auth {
		config.NoClientAuth = true
	} else {
		if s.PublicKeyLookupFunc == nil {
			return fmt.Errorf("public key lookup func is not provided")
		}

		if s.PreLoginFunc == nil {
			s.PreLoginFunc = s.defaultPreLoginFunc
		}

		config.PublicKeyCallback = func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			ctx := context.WithValue(context.Background(), UserContextKey{}, conn.User())
			err := s.PreLoginFunc(ctx, conn)
			if err != nil {
				return nil, err
			}

			log.Print(err)

			pkey, err := s.PublicKeyLookupFunc(ctx, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))))
			if err != nil {
				return nil, err
			}

			if pkey == nil {
				return nil, fmt.Errorf("auth handler did not return a key")
			}

			return &ssh.Permissions{Extensions: map[string]string{keyID: pkey.Id, keyName: pkey.Name, sshUser: conn.User()}}, nil
		}
	}

	keypath := s.config.KeyPath()
	if !fileExists(keypath) {
		if err := s.createServerKey(); err != nil {
			return err
		}
	}

	privateBytes, err := ioutil.ReadFile(keypath)
	if err != nil {
		return err
	}

	private, err := ssh.ParsePrivateKey(privateBytes)
	if err != nil {
		return err
	}

	config.AddHostKey(private)
	s.sshconfig = config
	return nil
}

func (s *SSH) Listen(bind string) error {
	if s.listener != nil {
		return ErrAlreadyStarted
	}

	if err := s.setup(); err != nil {
		return err
	}

	if err := s.config.Setup(); err != nil {
		return err
	}

	var err error
	s.listener, err = net.Listen("tcp", bind)
	if err != nil {
		return err
	}

	return nil
}

func (s *SSH) Serve() error {
	if s.listener == nil {
		return ErrNoListener
	}

	for {
		// wait for connection or Stop()
		conn, err := s.listener.Accept()
		if err != nil {
			return err
		}

		go func() {
			log.Printf("ssh: handshaking for %s", conn.RemoteAddr())

			sConn, chans, reqs, err := ssh.NewServerConn(conn, s.sshconfig)
			if err != nil {
				if err == io.EOF {
					log.Printf("ssh: handshaking was terminated: %v", err)
				} else {
					log.Printf("ssh: error on handshaking: %v", err)
				}
				return
			}

			log.Printf("ssh: connection from %s (%s)", sConn.RemoteAddr(), sConn.ClientVersion())

			var (
				pk      PublicKey
				gitUser string
			)

			if sConn.Permissions != nil {
				pk.Name = sConn.Permissions.Extensions[keyName]
				pk.Id = sConn.Permissions.Extensions[keyID]
				gitUser = sConn.Permissions.Extensions[sshUser]
			}

			ctx := context.WithValue(context.Background(), PublicKeyContextKey{}, pk)
			ctx = context.WithValue(ctx, UserContextKey{}, gitUser)

			go ssh.DiscardRequests(reqs)
			go s.handleConnection(ctx, chans)
		}()
	}
}

func (s *SSH) ListenAndServe(bind string) error {
	if err := s.Listen(bind); err != nil {
		return err
	}
	return s.Serve()
}

// Stop stops the server if it has been started, otherwise it is a no-op.
func (s *SSH) Stop() error {
	if s.listener == nil {
		return nil
	}
	defer func() {
		s.listener = nil
	}()

	return s.listener.Close()
}

// Address returns the network address of the listener. This is in
// particular useful when binding to :0 to get a free port assigned by
// the OS.
func (s *SSH) Address() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return ""
}

// SetSSHConfig can be used to set custom SSH Server settings.
func (s *SSH) SetSSHConfig(cfg *ssh.ServerConfig) {
	s.sshconfig = cfg
}

// SetListener can be used to set custom Listener.
func (s *SSH) SetListener(l net.Listener) {
	s.listener = l
}
