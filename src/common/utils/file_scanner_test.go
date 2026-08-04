package utils

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bridgecrewio/yor/src/common/structure"
	"github.com/stretchr/testify/assert"
)

func TestGetFileScanner(t *testing.T) {
	nonFound := structure.Lines{Start: -1, End: -1}

	t.Run("missing file yields a nil scanner and a usable closer", func(t *testing.T) {
		scanner, closeFile, lines := GetFileScanner(filepath.Join(t.TempDir(), "nope.yaml"), &nonFound)
		assert.Nil(t, scanner, "callers check this before scanning")
		assert.NotNil(t, closeFile)
		assert.Equal(t, &nonFound, lines)
		assert.NotPanics(t, closeFile)
	})

	t.Run("readable file scans and can be removed after closing", func(t *testing.T) {
		dir := t.TempDir()
		fp := filepath.Join(dir, "f.yaml")
		assert.NoError(t, os.WriteFile(fp, []byte("a\nb\n"), 0600))

		scanner, closeFile, _ := GetFileScanner(fp, &nonFound)
		assert.NotNil(t, scanner)
		var got []string
		for scanner.Scan() {
			got = append(got, scanner.Text())
		}
		assert.Equal(t, []string{"a", "b"}, got)

		closeFile()
		// The descriptor used to be leaked on every call, which on Windows also blocks removal.
		assert.NoError(t, os.Remove(fp))
	})
}
