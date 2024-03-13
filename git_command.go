package gitkit

import (
	"fmt"
	"regexp"
	"strings"
)

var gitCommandRegex = regexp.MustCompile(`^(git[-|\s]upload-pack|git[-|\s]upload-archive|git[-|\s]receive-pack) '(.*)'$`)

type GitCommand struct {
	Command  string
	Repo     string
	Original string
}

func ParseGitCommand(cmd string) (*GitCommand, error) {
	matches := gitCommandRegex.FindAllStringSubmatch(cmd, 1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("invalid git command")
	}

	result := &GitCommand{
		Original: cmd,
		Command:  matches[0][1],
		Repo:     parseRepoName(matches[0][2]),
	}

	return result, nil
}

func parseRepoName(s string) (repoName string) {
	repoPath, _ := strings.CutPrefix(s, "/")
	repoName, _ = strings.CutSuffix(repoPath, ".git")

	return
}
