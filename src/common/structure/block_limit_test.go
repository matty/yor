package structure

import (
	"fmt"
	"testing"

	"github.com/bridgecrewio/yor/src/common/tagging/tags"
	"github.com/stretchr/testify/assert"
)

func existingTags(n int) []tags.ITag {
	var out []tags.ITag
	for i := 0; i < n; i++ {
		out = append(out, &tags.Tag{Key: fmt.Sprintf("existing_%d", i), Value: "v"})
	}
	return out
}

// SpecialResourceTypes caps the number of tags on some resources. The cap was applied by
// slicing NewTags to limit-len(ExitingTags) without checking that a resource might already
// carry more tags than the limit allows.
func TestAddNewTagsRespectsSpecialResourceLimit(t *testing.T) {
	limit := SpecialResourceTypes["aws_db_proxy"]
	assert.Positive(t, limit)

	t.Run("more existing tags than the limit allows", func(t *testing.T) {
		b := &Block{Type: "aws_db_proxy", ExitingTags: existingTags(limit + 1)}
		assert.NotPanics(t, func() {
			b.AddNewTags([]tags.ITag{&tags.Tag{Key: "yor_trace", Value: "x"}})
		})
		assert.Empty(t, b.GetNewTags(), "there is no room left for new tags")
	})

	t.Run("exactly at the limit", func(t *testing.T) {
		b := &Block{Type: "aws_db_proxy", ExitingTags: existingTags(limit)}
		assert.NotPanics(t, func() {
			b.AddNewTags([]tags.ITag{&tags.Tag{Key: "yor_trace", Value: "x"}})
		})
		assert.Empty(t, b.GetNewTags())
	})

	t.Run("room for some but not all new tags", func(t *testing.T) {
		b := &Block{Type: "aws_db_proxy", ExitingTags: existingTags(limit - 1)}
		b.AddNewTags([]tags.ITag{
			&tags.Tag{Key: "a_tag", Value: "x"},
			&tags.Tag{Key: "b_tag", Value: "y"},
		})
		assert.Len(t, b.GetNewTags(), 1)
	})

	t.Run("under the limit keeps every new tag", func(t *testing.T) {
		b := &Block{Type: "aws_db_proxy", ExitingTags: existingTags(2)}
		b.AddNewTags([]tags.ITag{
			&tags.Tag{Key: "a_tag", Value: "x"},
			&tags.Tag{Key: "b_tag", Value: "y"},
		})
		assert.Len(t, b.GetNewTags(), 2)
	})

	t.Run("resources without a limit are unaffected", func(t *testing.T) {
		b := &Block{Type: "aws_s3_bucket", ExitingTags: existingTags(50)}
		b.AddNewTags([]tags.ITag{&tags.Tag{Key: "yor_trace", Value: "x"}})
		assert.Len(t, b.GetNewTags(), 1)
	})
}
