package gittag

import (
	"testing"
	"time"

	"github.com/bridgecrewio/yor/src/common/gitservice"
	"github.com/bridgecrewio/yor/src/common/structure"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/stretchr/testify/assert"
)

// GetLatestCommit returns nil by design when every line of the block was last touched by
// a bot - which is what a repo tagged by yor's own action looks like on the next run.
// hasNonTagChanges used to dereference it.
func TestHasNonTagChangesWithoutLatestCommit(t *testing.T) {
	tg := &TagGroup{}
	block := &structure.Block{
		Lines:    structure.Lines{Start: 1, End: 2},
		TagLines: structure.Lines{Start: -1, End: -1},
		Name:     "MyBucket",
	}

	cases := []struct {
		name  string
		lines map[int]*git.Line
	}{
		{"every line authored by a bot", map[int]*git.Line{
			1: {Author: "github-actions[bot]", Date: time.Now(), Hash: plumbing.ZeroHash},
			2: {Author: "dependabot[bot]", Date: time.Now(), Hash: plumbing.ZeroHash},
		}},
		{"a nil blame line", map[int]*git.Line{1: nil}},
		{"no blame lines at all", map[int]*git.Line{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blame := &gitservice.GitBlame{BlamesByLine: tc.lines}
			assert.Nil(t, blame.GetLatestCommit())
			assert.NotPanics(t, func() {
				assert.False(t, tg.hasNonTagChanges(blame, block))
			})
		})
	}
}

// A human-authored change outside the tags region is still reported.
func TestHasNonTagChangesDetectsRealChange(t *testing.T) {
	tg := &TagGroup{}
	hash := plumbing.NewHash("71f2c04c00ecf21b86d35c6c6ae71fe49aafc79c")
	blame := &gitservice.GitBlame{BlamesByLine: map[int]*git.Line{
		1: {Author: "someone@example.com", Date: time.Now(), Hash: hash},
		2: {Author: "someone@example.com", Date: time.Now().Add(-time.Hour), Hash: hash},
	}}
	block := &structure.Block{
		Lines:    structure.Lines{Start: 1, End: 2},
		TagLines: structure.Lines{Start: -1, End: -1},
		Name:     "MyBucket",
	}
	assert.NotNil(t, blame.GetLatestCommit())
	assert.True(t, tg.hasNonTagChanges(blame, block))
}

// A line missing from the origin->git map reads back as 0, which is not a valid 1-based
// git line but used to pass the "< 0" guard in CreateTagsForBlock.
func TestGetBlockLinesInGitUnmappedLines(t *testing.T) {
	tg := &TagGroup{}
	block := &structure.Block{Lines: structure.Lines{Start: 4, End: 6}}

	t.Run("nothing mapped", func(t *testing.T) {
		got := tg.getBlockLinesInGit(block, fileLineMapper{originToGit: map[int]int{}})
		assert.Equal(t, structure.Lines{Start: -1, End: -1}, got)
	})

	t.Run("all lines explicitly changed", func(t *testing.T) {
		got := tg.getBlockLinesInGit(block, fileLineMapper{originToGit: map[int]int{4: -1, 5: -1, 6: -1}})
		assert.Equal(t, structure.Lines{Start: -1, End: -1}, got)
	})

	t.Run("first and last mapped lines are used", func(t *testing.T) {
		got := tg.getBlockLinesInGit(block, fileLineMapper{originToGit: map[int]int{4: -1, 5: 11, 6: 12}})
		assert.Equal(t, structure.Lines{Start: 11, End: 12}, got)
	})
}

// updateBlameForOriginLines used to insert a nil whenever the blame result did not cover
// a line, which made GetLatestCommit abandon the whole block.
func TestUpdateBlameForOriginLinesSkipsUncoveredLines(t *testing.T) {
	tg := &TagGroup{}
	hash := plumbing.NewHash("71f2c04c00ecf21b86d35c6c6ae71fe49aafc79c")
	blame := &gitservice.GitBlame{
		GitUserEmail: "me@example.com",
		BlamesByLine: map[int]*git.Line{
			11: {Author: "someone@example.com", Date: time.Now(), Hash: hash},
		},
	}
	block := &structure.Block{Lines: structure.Lines{Start: 4, End: 6}, Name: "MyBucket"}
	// Line 5 maps to a git line the blame result does not cover, line 6 is uncommitted.
	fileMapping := map[int]int{4: 11, 5: 99, 6: -1}

	tg.updateBlameForOriginLines(block, blame, fileMapping)

	assert.NotContains(t, blame.BlamesByLine, 5)
	assert.Len(t, blame.BlamesByLine, 2)
	for lineNum, line := range blame.BlamesByLine {
		assert.NotNil(t, line, "line %d must not be nil", lineNum)
	}
	// A latest commit is still resolvable, which it was not when a nil was recorded.
	assert.NotNil(t, blame.GetLatestCommit())
}
