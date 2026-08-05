package structure

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// hclwrite parses a block header without validating it against a schema, so a resource
// block with no labels reaches the parser. It used to panic on Labels()[0], which took
// down the whole directory scan because one file was malformed.
func TestParseFileBlockWithoutLabels(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"resource with no labels", "resource {\n  bucket = \"b\"\n}\n"},
		{"resource with only a type label", "resource \"aws_s3_bucket\" {\n  bucket = \"b\"\n}\n"},
		{"module with no labels", "module {\n  source = \"./x\"\n}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			fp := filepath.Join(dir, "main.tf")
			assert.NoError(t, os.WriteFile(fp, []byte(tc.src), 0600))

			p := &TerraformParser{}
			p.Init(dir, nil)
			assert.NotPanics(t, func() {
				_, err := p.ParseFile(fp)
				// The file itself is parseable; the malformed block is skipped.
				assert.NoError(t, err)
			})
		})
	}
}

// A well formed resource alongside a malformed one must still be tagged.
func TestParseFileSkipsOnlyTheMalformedBlock(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "main.tf")
	src := `resource {
  bucket = "broken"
}

resource "aws_s3_bucket" "good" {
  bucket = "good"
}
`
	assert.NoError(t, os.WriteFile(fp, []byte(src), 0600))

	p := &TerraformParser{}
	p.Init(dir, nil)
	blocks, err := p.ParseFile(fp)
	assert.NoError(t, err)
	assert.Len(t, blocks, 1)
	assert.Equal(t, "aws_s3_bucket.good", blocks[0].GetResourceID())
}
