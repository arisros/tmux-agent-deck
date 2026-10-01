package git

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBranch(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	write(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/feat/oauth\n")
	write(t, filepath.Join(repo, "internal", "deep", "file.go"), "")

	// A linked work tree: .git is a file pointing into the main repository.
	tree := filepath.Join(root, "tree")
	write(t, filepath.Join(repo, ".git", "worktrees", "tree", "HEAD"), "ref: refs/heads/fix/bug\n")
	write(t, filepath.Join(tree, ".git"), "gitdir: "+filepath.Join(repo, ".git", "worktrees", "tree")+"\n")
	write(t, filepath.Join(tree, "src", "x"), "")

	// A submodule: the gitdir is relative.
	sub := filepath.Join(repo, "vendor", "lib")
	write(t, filepath.Join(repo, ".git", "modules", "lib", "HEAD"), "0123456789abcdef0123456789abcdef01234567\n")
	write(t, filepath.Join(sub, ".git"), "gitdir: ../../.git/modules/lib\n")

	broken := filepath.Join(root, "broken")
	write(t, filepath.Join(broken, ".git", "HEAD"), "not a head\n")

	for dir, want := range map[string]string{
		repo:                                     "feat/oauth",
		filepath.Join(repo, "internal", "deep"):  "feat/oauth",
		tree:                                     "fix/bug",
		filepath.Join(tree, "src"):               "fix/bug",
		sub:                                      "0123456",
		broken:                                   "",
		filepath.Join(root, "nowhere", "at-all"): "",
		"":                                       "",
	} {
		if got := Branch(dir); got != want {
			t.Errorf("Branch(%s) = %q, want %q", dir, got, want)
		}
	}
}
