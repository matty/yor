package gitservice

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Git records tree paths with forward slashes. filepath.Join yields the OS separator, so
// on Windows this returned "terraform\aws\s3.tf" and git.Blame answered "file not found",
// which meant no resource below the scan root could be git tagged.
//
// The pre-existing tests in this package assert forward slashes too, but they need a
// network clone; this one is a plain unit test.
func TestComputeRelativeFilePathUsesForwardSlashes(t *testing.T) {
	t.Run("path under the git root", func(t *testing.T) {
		root := filepath.Join("repo", "root")
		g := &GitService{gitRootDir: root, scanPathFromRoot: filepath.Join("terraform", "aws")}
		got := g.ComputeRelativeFilePath(filepath.Join(root, "s3.tf"))
		assert.Equal(t, "terraform/aws/s3.tf", got)
		assert.NotContains(t, got, "\\")
	})

	t.Run("path outside the git root", func(t *testing.T) {
		g := &GitService{gitRootDir: filepath.Join("repo", "root"), scanPathFromRoot: "terraform"}
		got := g.ComputeRelativeFilePath(filepath.Join("aws", "db-app.tf"))
		assert.Equal(t, "terraform/aws/db-app.tf", got)
		assert.NotContains(t, got, "\\")
	})

	t.Run("no scan sub path", func(t *testing.T) {
		root := filepath.Join("repo", "root")
		g := &GitService{gitRootDir: root, scanPathFromRoot: ""}
		got := g.ComputeRelativeFilePath(filepath.Join(root, "main.tf"))
		assert.Equal(t, "main.tf", got)
	})
}
