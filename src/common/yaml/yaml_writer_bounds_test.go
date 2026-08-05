package yaml

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bridgecrewio/yor/src/common/structure"
	"github.com/bridgecrewio/yor/src/common/tagging/tags"
	"github.com/stretchr/testify/assert"
)

// A serverless.yml whose first line is "functions:" has no line above the token to look
// for a #yor:skipall comment on.
func TestMapResourcesLineYAMLTokenOnFirstLine(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "serverless.yml")
	assert.NoError(t, os.WriteFile(fp, []byte("functions:\n  hello:\n    handler: handler.hello\n"), 0600))

	assert.NotPanics(t, func() {
		lines, skipped := MapResourcesLineYAML(fp, []string{"hello"}, "functions")
		assert.NotNil(t, lines)
		assert.Empty(t, skipped)
	})
}

func TestMapResourcesLineYAMLSkipAllOnFirstLine(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "serverless.yml")
	content := "#yor:skipall\nfunctions:\n  hello:\n    handler: handler.hello\n"
	assert.NoError(t, os.WriteFile(fp, []byte(content), 0600))

	_, skipped := MapResourcesLineYAML(fp, []string{"hello"}, "functions")
	assert.Equal(t, []string{"hello"}, skipped)
}

// WriteYAMLFile rebuilds the whole resources region from the per-block line ranges, so a
// range it cannot resolve must abort the write rather than silently drop those lines.
func TestWriteYAMLFileRefusesUnresolvableLines(t *testing.T) {
	dir := t.TempDir()
	readPath := filepath.Join(dir, "serverless.yml")
	original := "service: test\n\nfunctions:\n  hello:\n    handler: handler.hello\n"
	assert.NoError(t, os.WriteFile(readPath, []byte(original), 0600))
	writePath := filepath.Join(dir, "out.yml")

	cases := []struct {
		name  string
		lines structure.Lines
	}{
		{"unmapped resource", structure.Lines{Start: -1, End: -1}},
		{"end before start", structure.Lines{Start: 4, End: 2}},
		{"end past the file", structure.Lines{Start: 3, End: 900}},
		{"single line resource", structure.Lines{Start: 3, End: 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blocks := []structure.IBlock{&CustomBlock{Block: structure.Block{
				FilePath:          readPath,
				Name:              "hello",
				IsTaggable:        true,
				TagsAttributeName: "tags",
				NewTags:           []tags.ITag{&tags.Tag{Key: "new_tag", Value: "new_value"}},
				RawBlock:          structure.Function{Name: "hello"},
				Lines:             tc.lines,
				TagLines:          structure.Lines{Start: -1, End: -1},
			}}}

			var err error
			assert.NotPanics(t, func() {
				err = WriteYAMLFile(readPath, blocks, writePath, "tags", "functions")
			})
			assert.Error(t, err)
			assert.NoFileExists(t, writePath, "nothing should be written when the lines do not resolve")

			unchanged, readErr := os.ReadFile(readPath)
			assert.NoError(t, readErr)
			assert.Equal(t, original, string(unchanged))
		})
	}
}

func TestWriteYAMLFileWithNoBlocks(t *testing.T) {
	dir := t.TempDir()
	readPath := filepath.Join(dir, "serverless.yml")
	assert.NoError(t, os.WriteFile(readPath, []byte("functions:\n  hello:\n    handler: h\n"), 0600))

	assert.NotPanics(t, func() {
		err := WriteYAMLFile(readPath, []structure.IBlock{}, filepath.Join(dir, "out.yml"), "tags", "functions")
		assert.Error(t, err)
	})
}
