package gitservice

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/bridgecrewio/yor/src/common/logger"
	"github.com/bridgecrewio/yor/src/common/structure"

	"github.com/go-git/go-git/v5"
)

type GitBlame struct {
	GitOrg        string
	GitRepository string
	BlamesByLine  map[int]*git.Line
	FilePath      string
	GitUserEmail  string
}

func GetPreviousBlameResult(gitSvc *GitService, filePath string) (*git.BlameResult, *object.Commit) {
	if gitSvc.repository == nil {
		return nil, nil
	}
	ref, err := gitSvc.repository.Head()
	if err != nil {
		return nil, nil
	}
	commit, err := gitSvc.repository.CommitObject(ref.Hash())
	if err != nil {
		return nil, nil
	}
	parentIter := commit.Parents()
	previousCommit, err := parentIter.Next()
	if err != nil {
		return nil, nil
	}

	var previousBlameResult *git.BlameResult
	result, ok := gitSvc.PreviousBlameByFile.Load(filePath)
	if !ok {
		return nil, nil
	}

	previousBlameResult = result.(*git.BlameResult)
	return previousBlameResult, previousCommit
}

func NewGitBlame(relativeFilePath string, filePath string, lines structure.Lines, blameResult *git.BlameResult, gitSvc *GitService) *GitBlame {
	gitBlame := GitBlame{GitOrg: gitSvc.organization, GitRepository: gitSvc.repoName, BlamesByLine: map[int]*git.Line{}, FilePath: relativeFilePath, GitUserEmail: gitSvc.currentUserEmail}
	if blameResult == nil {
		logger.Warning(fmt.Sprintf("got no git blame for file %s", relativeFilePath))
		return &gitBlame
	}
	startLine := lines.Start - 1 // the lines in blameResult.Lines start from zero while the lines range start from 1
	endLine := lines.End - 1
	// Only the upper bound used to be checked. A resource whose lines could not be
	// mapped carries {-1,-1}, and an unresolved origin->git lookup yields 0; both turn
	// into a negative index into blameResult.Lines.
	if startLine < 0 || endLine < startLine {
		logger.Warning(fmt.Sprintf("cannot get git blame for %s: unresolved line range %d-%d", relativeFilePath, lines.Start, lines.End))
		return &gitBlame
	}
	previousBlameResult, previousCommit := GetPreviousBlameResult(gitSvc, filePath)

	for line := startLine; line <= endLine; line++ {
		if line >= len(blameResult.Lines) {
			logger.Warning(fmt.Sprintf("Index out of bound on parsed file %s", relativeFilePath))
			return &gitBlame
		}
		blameLine := blameResult.Lines[line]
		if blameLine == nil {
			continue
		}
		gitBlame.BlamesByLine[line+1] = blameLine

		// Check if the line has been removed in the current state of the file
		if previousBlameResult != nil && len(previousBlameResult.Lines) > len(blameResult.Lines) && previousCommit != nil {
			previousLine := previousBlameResult.Lines[line]
			if previousLine != nil && previousLine.Text != blameLine.Text {
				// The line has been removed, so update the git commit id
				blameLine.Hash = previousCommit.Hash
			}
		}
	}

	return &gitBlame
}

func (g *GitBlame) GetLatestCommit() (latestCommit *git.Line) {
	if g == nil {
		// GetBlameForFileLines returns a nil blame alongside its error; callers that only
		// check one of the two used to fault here instead of reporting the error.
		return nil
	}
	latestDate := time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC)
	for _, v := range g.BlamesByLine {
		if v == nil {
			// This line was added/edited but not committed yet, so latest commit is nil
			return nil
		}
		if latestDate.Before(v.Date) &&
			// Commit was not made by CI, i.e. github actions (for now)
			!strings.Contains(v.Author, "[bot]") && !strings.Contains(v.Author, "github-actions") {
			latestDate = v.Date
			latestCommit = v
		}
	}
	return
}
