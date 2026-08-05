package gitservice

import (
	"testing"

	"github.com/bridgecrewio/yor/src/common/structure"
	"github.com/go-git/go-git/v5"
	"github.com/stretchr/testify/assert"
)

// NewGitBlame only guarded the upper bound of the line range. A resource whose lines
// could not be mapped carries {-1,-1}, and an unresolved origin->git lookup yields 0;
// both became negative indexes into blameResult.Lines.
func TestNewGitBlameUnresolvedLineRanges(t *testing.T) {
	svc := &GitService{}
	blameResult := &git.BlameResult{Lines: []*git.Line{{Author: "a"}, {Author: "b"}}}

	cases := []struct {
		name  string
		lines structure.Lines
	}{
		{"unmapped resource", structure.Lines{Start: -1, End: -1}},
		{"zero start", structure.Lines{Start: 0, End: 2}},
		{"end before start", structure.Lines{Start: 2, End: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.NotPanics(t, func() {
				gb := NewGitBlame("f.yaml", "f.yaml", tc.lines, blameResult, svc)
				assert.NotNil(t, gb)
				assert.Empty(t, gb.BlamesByLine)
			})
		})
	}
}

func TestNewGitBlameNilInputs(t *testing.T) {
	svc := &GitService{}

	t.Run("nil blame result", func(t *testing.T) {
		assert.NotPanics(t, func() {
			gb := NewGitBlame("f.yaml", "f.yaml", structure.Lines{Start: 1, End: 2}, nil, svc)
			assert.Empty(t, gb.BlamesByLine)
		})
	})

	t.Run("nil line inside the blame result", func(t *testing.T) {
		blameResult := &git.BlameResult{Lines: []*git.Line{{Author: "a"}, nil, {Author: "c"}}}
		assert.NotPanics(t, func() {
			gb := NewGitBlame("f.yaml", "f.yaml", structure.Lines{Start: 1, End: 3}, blameResult, svc)
			// The nil line is skipped rather than recorded.
			assert.Len(t, gb.BlamesByLine, 2)
			assert.NotContains(t, gb.BlamesByLine, 2)
		})
	})
}

// A block that sits entirely within the blame result is still mapped as before.
func TestNewGitBlameHappyPath(t *testing.T) {
	svc := &GitService{}
	blameResult := &git.BlameResult{Lines: []*git.Line{{Author: "a"}, {Author: "b"}, {Author: "c"}}}
	gb := NewGitBlame("f.yaml", "f.yaml", structure.Lines{Start: 1, End: 3}, blameResult, svc)
	assert.Len(t, gb.BlamesByLine, 3)
	assert.Equal(t, "a", gb.BlamesByLine[1].Author)
	assert.Equal(t, "c", gb.BlamesByLine[3].Author)
}
