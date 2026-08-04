package yaml

import (
	"testing"

	"github.com/bridgecrewio/yor/src/common/tagging/tags"
	"github.com/stretchr/testify/assert"
)

// Key and Value can appear in either order within a CFN tag list entry, so each entry
// has to be resolved as a unit. Carrying a pending Value line across entries wrote an
// updated tag's value onto the preceding tag's Value line and left its own stale.
func TestUpdateExistingCFNTags(t *testing.T) {
	t.Run("second tag updated, first left alone", func(t *testing.T) {
		lines := []string{
			"        - Key: yor_trace",
			"          Value: keep-me",
			"        - Key: yor_name",
			"          Value: stale",
		}
		UpdateExistingCFNTags(lines, []*tags.TagDiff{{Key: "yor_name", PrevValue: "stale", NewValue: "correct"}})
		assert.Equal(t, "          Value: keep-me", lines[1])
		assert.Equal(t, "          Value: correct", lines[3])
	})

	t.Run("quoted keys", func(t *testing.T) {
		lines := []string{
			`        - Key: "isSpecial"`,
			`          Value: "true"`,
			`        - Key: "yor_name"`,
			`          Value: "stale"`,
		}
		UpdateExistingCFNTags(lines, []*tags.TagDiff{{Key: "yor_name", PrevValue: "stale", NewValue: "correct"}})
		assert.Equal(t, `          Value: "true"`, lines[1])
		assert.Equal(t, "          Value: correct", lines[3])
	})

	t.Run("value before key", func(t *testing.T) {
		lines := []string{
			"        - Value: stale",
			"          Key: yor_name",
		}
		UpdateExistingCFNTags(lines, []*tags.TagDiff{{Key: "yor_name", PrevValue: "stale", NewValue: "correct"}})
		assert.Equal(t, "        - Value: correct", lines[0])
	})

	t.Run("a tag key containing regex metacharacters does not blow up", func(t *testing.T) {
		lines := []string{
			"        - Key: bad(key",
			"          Value: stale",
		}
		assert.NotPanics(t, func() {
			UpdateExistingCFNTags(lines, []*tags.TagDiff{{Key: "bad(key", PrevValue: "stale", NewValue: "correct"}})
		})
		assert.Equal(t, "          Value: correct", lines[1])
	})
}
