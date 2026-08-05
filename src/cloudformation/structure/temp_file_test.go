package structure

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bridgecrewio/yor/src/common/structure"
	"github.com/stretchr/testify/assert"
)

// WriteFile writes a candidate result to a temp file and re-parses it before touching
// the real file. That temp file must never survive the call: it is a valid template, so
// a leftover gets picked up and tagged by the next run, which then leaks another one.
func TestWriteFileLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "template.json")
	template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Bucket": {
      "Type": "AWS::S3::Bucket",
      "Properties": {
        "BucketName": "my-bucket"
      }
    }
  }
}`
	assert.NoError(t, os.WriteFile(templatePath, []byte(template), 0600))

	p := &CloudformationParser{}
	p.Init(dir, nil)
	blocks, err := p.ParseFile(templatePath)
	assert.NoError(t, err)
	assert.NotEmpty(t, blocks)

	assert.NoError(t, p.WriteFile(templatePath, blocks, templatePath))

	entries, err := os.ReadDir(dir)
	assert.NoError(t, err)
	var leftovers []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "temp.") {
			leftovers = append(leftovers, e.Name())
		}
	}
	assert.Empty(t, leftovers, "WriteFile left temp files behind")
}

// The temp file used to be removed in a defer registered before the error from
// os.CreateTemp was checked, so a failure to create it panicked on a nil *os.File.
func TestWriteFileWhenTempFileCannotBeCreated(t *testing.T) {
	p := &CloudformationParser{}
	p.Init(t.TempDir(), nil)
	unwritable := filepath.Join(t.TempDir(), "does-not-exist", "template.json")

	assert.NotPanics(t, func() {
		err := p.WriteFile(unwritable, []structure.IBlock{}, unwritable)
		assert.Error(t, err)
	})
}
