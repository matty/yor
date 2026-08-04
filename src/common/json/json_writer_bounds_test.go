package json

import (
	"strings"
	"testing"

	"github.com/bridgecrewio/yor/src/common/structure"
	"github.com/bridgecrewio/yor/src/common/tagging/tags"
	"github.com/stretchr/testify/assert"
)

func newTestBlock(existing []tags.ITag, newTags []tags.ITag) *structure.Block {
	return &structure.Block{
		FilePath:          "template.json",
		Name:              "Bucket",
		Type:              "AWS::S3::Bucket",
		IsTaggable:        true,
		TagsAttributeName: "Tags",
		ExitingTags:       existing,
		NewTags:           newTags,
	}
}

// A brace inside a string value must not be counted as a JSON bracket: it throws the
// open/close pairing off and used to pop an empty stack.
func TestMapBracketsSkipsStringLiterals(t *testing.T) {
	t.Run("closing braces inside a string value", func(t *testing.T) {
		str := `{"Description": "closes with }}", "a": 1}`
		brackets := MapBracketsInString(str)
		assert.Len(t, brackets, 2)
		pairs := GetBracketsPairs(brackets)
		assert.Len(t, pairs, 1)
		assert.Equal(t, 0, pairs[0].Open.CharIndex)
		assert.Equal(t, len(str)-1, pairs[0].Close.CharIndex)
	})
	t.Run("escaped quote inside a string value", func(t *testing.T) {
		str := `{"a": "say \" }", "b": 1}`
		pairs := GetBracketsPairs(MapBracketsInString(str))
		assert.Len(t, pairs, 1)
	})
	t.Run("brackets inside a string still count lines", func(t *testing.T) {
		str := "{\n\"a\": \"}\",\n\"b\": {}\n}"
		brackets := MapBracketsInString(str)
		assert.Len(t, brackets, 4)
		assert.Equal(t, 3, brackets[1].Line)
	})
}

// A JSON file with no trailing newline yields a bracket line number equal to the
// number of lines, which used to index off the end of the split file.
func TestGetKeyIndexOutOfRangeLines(t *testing.T) {
	src := `{"Resources":{"R":{"Type":"AWS::S3::Bucket"}}}`
	pairs := GetBracketsPairs(MapBracketsInString(src))
	assert.NotPanics(t, func() {
		FindScopeInJSON(src, "Type", pairs, &structure.Lines{Start: 1, End: 1})
	})
	assert.NotPanics(t, func() {
		FindScopeInJSON(src, "Type", pairs, &structure.Lines{Start: 99, End: 120})
	})
}

func TestFindIndentBounds(t *testing.T) {
	assert.Equal(t, "", findIndent("no stop char here", '{', 0))
	assert.Equal(t, "", findIndent("{}", '{', -1))
	assert.Equal(t, "", findIndent("", '{', 0))
	assert.Equal(t, "", findIndent("abc", '{', 99))
}

// Re-tagging a template where every tag key already exists and only values changed:
// diff.Added is empty, so there are no tag lines to splice in.
func TestAddTagsUpdatedValuesOnly(t *testing.T) {
	src := `{
  "Resources": {
    "Bucket": {
      "Type": "AWS::S3::Bucket",
      "Properties": {
        "Tags": [
          {
            "Key": "yor_trace",
            "Value": "old-value"
          }
        ]
      }
    }
  }
}`
	block := newTestBlock(
		[]tags.ITag{&tags.Tag{Key: "yor_trace", Value: "old-value"}},
		[]tags.ITag{&tags.Tag{Key: "yor_trace", Value: "new-value"}},
	)
	diff := block.CalculateTagsDiff()
	assert.Empty(t, diff.Added)
	assert.Len(t, diff.Updated, 1)

	out := AddTagsToResourceStr(src, block, GetBracketsPairs(MapBracketsInString(src)))
	assert.Contains(t, out, `"Value": "new-value"`)
	assert.NotContains(t, out, "old-value")
	assert.NotContains(t, out, "null")
	// The resource scope must come back, not the whole file.
	assert.True(t, strings.HasPrefix(out, "{"))
	assert.NotContains(t, out, `"Resources"`)
}

// An empty tags array has no existing entry to copy the layout from.
func TestAddTagsToEmptyTagsArray(t *testing.T) {
	src := `{
  "Resources": {
    "Bucket": {
      "Type": "AWS::S3::Bucket",
      "Properties": {
        "Tags": []
      }
    }
  }
}`
	block := newTestBlock(nil, []tags.ITag{&tags.Tag{Key: "yor_trace", Value: "abc"}})
	out := AddTagsToResourceStr(src, block, GetBracketsPairs(MapBracketsInString(src)))
	assert.Contains(t, out, `"Key": "yor_trace"`)
	assert.Contains(t, out, `"Value": "abc"`)
	assert.NotContains(t, out, "[]")
}

// "Tags": [{ ... }] with the brace on the same line as the array leaves findIndent
// with an empty indent, which used to be sliced to -1.
func TestAddTagsBraceOnSameLineAsArray(t *testing.T) {
	src := `{
  "Resources": {
    "Bucket": {
      "Type": "AWS::S3::Bucket",
      "Properties": {
        "Tags": [{
          "Key": "existing",
          "Value": "v"
        }]
      }
    }
  }
}`
	block := newTestBlock(
		[]tags.ITag{&tags.Tag{Key: "existing", Value: "v"}},
		[]tags.ITag{&tags.Tag{Key: "yor_trace", Value: "abc"}},
	)
	assert.NotPanics(t, func() {
		out := AddTagsToResourceStr(src, block, GetBracketsPairs(MapBracketsInString(src)))
		assert.Contains(t, out, "yor_trace")
	})
}

func TestTrimIndentBy(t *testing.T) {
	assert.Equal(t, "  ", trimIndentBy("   ", 1))
	assert.Equal(t, "   ", trimIndentBy("   ", 0))
	assert.Equal(t, "   ", trimIndentBy("   ", -4))
	assert.Equal(t, "", trimIndentBy("  ", 9))
	assert.Equal(t, "", trimIndentBy("", 1))
}
