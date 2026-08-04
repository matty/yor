package utils

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCreateClosedTempFile(t *testing.T) {
	t.Run("returns a path that can be removed straight away", func(t *testing.T) {
		dir := t.TempDir()
		tempFilePath, err := CreateClosedTempFile(dir, "temp.*.template")
		assert.NoError(t, err)
		assert.FileExists(t, tempFilePath)

		// The whole point: with a handle still open this fails on Windows, which is how
		// temp.* files ended up accumulating in the directory being tagged.
		assert.NoError(t, os.Remove(tempFilePath))

		entries, err := os.ReadDir(dir)
		assert.NoError(t, err)
		assert.Empty(t, entries)
	})

	t.Run("reports an error instead of panicking when the dir does not exist", func(t *testing.T) {
		missingDir := filepath.Join(t.TempDir(), "does-not-exist")
		tempFilePath, err := CreateClosedTempFile(missingDir, "temp.*.template")
		assert.Error(t, err)
		assert.Empty(t, tempFilePath)
	})
}
