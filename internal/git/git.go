// Package git reads the checked-out branch of a directory straight from the
// repository's own files. No git process runs, so a view can ask for every
// agent on every refresh.
package git

import (
	"os"
	"path/filepath"
	"strings"
)

// Branch is the branch checked out in the work tree that holds dir: "main",
// "feat/oauth", or the first seven characters of a detached commit. It is ""
// outside a repository, or when the files are not what git writes.
func Branch(dir string) string {
	if dir == "" {
		return ""
	}
	for i := 0; i < 64; i++ {
		if head := headFile(filepath.Join(dir, ".git")); head != "" {
			return parseHead(head)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// headFile returns the content of HEAD for a .git entry: a directory in a
// plain clone, a "gitdir: <path>" file in a linked work tree or a submodule.
func headFile(dotGit string) string {
	fi, err := os.Stat(dotGit)
	if err != nil {
		return ""
	}
	gitDir := dotGit
	if !fi.IsDir() {
		b, err := os.ReadFile(dotGit)
		if err != nil {
			return ""
		}
		target, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
		if !ok {
			return ""
		}
		gitDir = strings.TrimSpace(target)
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(filepath.Dir(dotGit), gitDir)
		}
	}
	b, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func parseHead(head string) string {
	if ref, ok := strings.CutPrefix(head, "ref:"); ok {
		return strings.TrimPrefix(strings.TrimSpace(ref), "refs/heads/")
	}
	if len(head) >= 7 && strings.Trim(head, "0123456789abcdef") == "" {
		return head[:7]
	}
	return ""
}
