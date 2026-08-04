package yaml

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/bridgecrewio/yor/src/common/logger"
	"github.com/bridgecrewio/yor/src/common/structure"
	"github.com/bridgecrewio/yor/src/common/tagging/tags"
	"github.com/bridgecrewio/yor/src/common/utils"
	"github.com/sanathkr/yaml"
)

const SingleIndent = "  "

func WriteYAMLFile(readFilePath string, blocks []structure.IBlock, writeFilePath string, tagsAttributeName string, resourcesStartToken string) error {
	// #nosec G304
	// read file bytes
	originFileSrc, err := os.ReadFile(readFilePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s because %s", readFilePath, err)
	}
	if len(blocks) == 0 {
		return fmt.Errorf("got no blocks to write to file %s", readFilePath)
	}
	yamlBlock, ok := blocks[0].(IYamlBlock)
	if !ok {
		return fmt.Errorf("block of type %T in file %s is not a yaml block", blocks[0], readFilePath)
	}
	isCfn := yamlBlock.GetFramework() == "Cloudformation"
	originLines := utils.GetLinesFromBytes(originFileSrc)

	// This function rebuilds the whole resources region from the per-block line ranges,
	// so a range that does not resolve cannot simply be skipped - the lines it covers
	// would be dropped from the output. Resources whose lines could not be mapped carry
	// {-1,-1}, which used to slice out of range. Bail out before writing anything and
	// leave the file untouched instead.
	if err = validateBlockLines(blocks, originLines, readFilePath); err != nil {
		return err
	}

	oldResourcesLineRange := computeResourcesLineRange(originLines, blocks, isCfn)
	if oldResourcesLineRange.Start < 0 || oldResourcesLineRange.End < oldResourcesLineRange.Start ||
		oldResourcesLineRange.End >= len(originLines) {
		return fmt.Errorf("could not resolve the %s block of file %s (lines %d-%d), leaving it untouched",
			resourcesStartToken, readFilePath, oldResourcesLineRange.Start, oldResourcesLineRange.End)
	}
	resourcesLines := make([]string, 0)
	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].GetLines().Start < blocks[j].GetLines().Start
	})
	linesPerTag := 1
	if isCfn {
		linesPerTag = 2
	}
	for _, resourceBlock := range blocks {
		rawBlock := resourceBlock.GetRawBlock()
		newResourceLines := getYAMLLines(rawBlock)
		newResourceTagLineRange, _ := FindTagsLinesYAML(newResourceLines, tagsAttributeName)
		oldResourceLinesRange := resourceBlock.GetLines()
		oldResourceLines := originLines[oldResourceLinesRange.Start : oldResourceLinesRange.End+1]

		// if the block is not taggable, write it and continue
		if !resourceBlock.IsBlockTaggable() {
			resourcesLines = append(resourcesLines, oldResourceLines...)
			continue
		}

		oldResourceTagLines := resourceBlock.GetTagsLines()
		// if the resource doesn't contain Tags entry - create it
		if oldResourceTagLines.Start == -1 || oldResourceTagLines.End == -1 {
			// get the indentation of the property under the resource name
			tagAttributeIndent := ExtractIndentationOfLine(oldResourceLines[1])
			if isCfn {
				tagAttributeIndent += SingleIndent
			}
			lastIndex := -1
			for i, line := range oldResourceLines {
				if len(ExtractIndentationOfLine(line)) < len(tagAttributeIndent) {
					continue
				}
				lastIndex = i
			}
			resourcesLines = append(resourcesLines, oldResourceLines[:lastIndex+1]...)
			resourcesLines = append(resourcesLines, tagAttributeIndent+tagsAttributeName+":") // add the 'Tags:' line
			tagIndent := tagAttributeIndent
			if isCfn {
				tagIndent += SingleIndent
			}
			resourcesLines = append(resourcesLines, IndentLines(newResourceLines[newResourceTagLineRange.Start+1:newResourceTagLineRange.End+1], tagIndent, 0)...)
			resourcesLines = append(resourcesLines, oldResourceLines[lastIndex+1:]...)
			continue
		}

		oldTagsIndent := ExtractIndentationOfLine(oldResourceLines[oldResourceTagLines.Start-oldResourceLinesRange.Start])
		oldTagsValueIndent := len(ExtractIndentationOfLine(oldResourceLines[oldResourceTagLines.Start-oldResourceLinesRange.Start+1])) - len(oldTagsIndent)
		if isCfn {
			oldTagsValueIndent = 0
		}
		if isCfn {
			oldTagsIndent += SingleIndent
		}
		resourcesLines = append(resourcesLines, oldResourceLines[:oldResourceTagLines.Start-oldResourceLinesRange.Start]...) // add all the resource's line before the tags
		tagLines := oldResourceLines[oldResourceTagLines.Start-oldResourceLinesRange.Start : oldResourceTagLines.End-oldResourceLinesRange.Start+1]
		diff := resourceBlock.CalculateTagsDiff()
		if isCfn {
			UpdateExistingCFNTags(tagLines, diff.Updated)
		} else {
			UpdateExistingSLSTags(tagLines, diff.Updated)
		}
		allNewResourceTagLines := IndentLines(newResourceLines[newResourceTagLineRange.Start+1:newResourceTagLineRange.End+1], oldTagsIndent, oldTagsValueIndent)
		var netNewResourceLines []string
		for i := 0; i+linesPerTag <= len(allNewResourceTagLines); i += linesPerTag {
			l := allNewResourceTagLines[i]
			key := getKeyFromLine(l, isCfn)
			if key == "" {
				continue
			}
			found := false
			for _, tag := range diff.Added {
				if tag.GetKey() == key {
					found = true
					break
				}
			}
			if found {
				netNewResourceLines = append(netNewResourceLines, allNewResourceTagLines[i:i+linesPerTag]...)
			}
		}
		resourcesLines = append(resourcesLines, tagLines...)            // Add old tags
		resourcesLines = append(resourcesLines, netNewResourceLines...) // Add new tags
		// Add any other attributes after the tags
		resourcesLines = append(resourcesLines, oldResourceLines[oldResourceTagLines.End-oldResourceLinesRange.Start+1:]...)
	}
	allLines := make([]string, oldResourcesLineRange.Start)
	copy(allLines, originLines[:oldResourcesLineRange.Start])
	if !isCfn {
		allLines = append(allLines, resourcesStartToken+":")
	}
	allLines = append(allLines, resourcesLines...)
	allLines = append(allLines, originLines[oldResourcesLineRange.End+1:]...)
	linesText := strings.Join(allLines, "\n")

	err = os.WriteFile(writeFilePath, []byte(linesText), 0600)

	return err
}

// validateBlockLines reports an error if any block's recorded line range does not sit
// inside originLines, or is too short to insert a tags attribute into.
func validateBlockLines(blocks []structure.IBlock, originLines []string, filePath string) error {
	for _, block := range blocks {
		lines := block.GetLines()
		if lines.Start < 0 || lines.End < lines.Start || lines.End >= len(originLines) {
			return fmt.Errorf("could not locate resource %s in file %s (lines %d-%d), leaving the file untouched",
				block.GetResourceID(), filePath, lines.Start, lines.End)
		}
		if !block.IsBlockTaggable() {
			continue
		}
		// A taggable resource needs at least a name line and one property line: the
		// indentation for the tags attribute is taken from the second line.
		if lines.End-lines.Start < 1 {
			return fmt.Errorf("resource %s in file %s spans a single line (%d), which leaves nowhere to add tags; leaving the file untouched",
				block.GetResourceID(), filePath, lines.Start)
		}
		tagLines := block.GetTagsLines()
		if tagLines.Start == -1 || tagLines.End == -1 {
			continue
		}
		if tagLines.Start < lines.Start || tagLines.End > lines.End || tagLines.End < tagLines.Start {
			return fmt.Errorf("tags of resource %s in file %s are recorded at lines %d-%d, outside the resource (%d-%d); leaving the file untouched",
				block.GetResourceID(), filePath, tagLines.Start, tagLines.End, lines.Start, lines.End)
		}
		// The line after the tags attribute is read to work out the value indentation.
		if tagLines.Start+1 > lines.End {
			return fmt.Errorf("tags of resource %s in file %s end the resource at line %d, leaving no value to indent against; leaving the file untouched",
				block.GetResourceID(), filePath, tagLines.Start)
		}
	}

	return nil
}

func getKeyFromLine(l string, isCfn bool) string {
	if isCfn {
		if strings.Contains(l, " Key:") {
			return strings.ReplaceAll(strings.ReplaceAll(l, " ", ""), "-Key:", "")
		}
	} else {
		return strings.Split(strings.ReplaceAll(strings.ReplaceAll(l, " ", ""), "-", ""), ":")[0]
	}
	return ""
}

func UpdateExistingCFNTags(tagsLinesList []string, diff []*tags.TagDiff) {
	currentValueLine := -1
	valueToSet := ""

	for i, tagLine := range tagsLinesList {
		if strings.Contains(tagLine, ` Key:`) {
			for _, tag := range diff {
				keyr := regexp.MustCompile(`\b` + tag.Key + `\b`)
				if keyr.Match([]byte(tagLine)) {
					if currentValueLine > -1 {
						tagsLinesList[currentValueLine] = ReplaceTagValue(tagsLinesList[currentValueLine], tag.NewValue)
						currentValueLine = -1
					} else {
						valueToSet = tag.NewValue
					}
					continue
				}
			}
		}
		if strings.Contains(tagLine, ` Value:`) {
			if valueToSet != "" {
				tagsLinesList[i] = ReplaceTagValue(tagLine, valueToSet)
				valueToSet = ""
			} else {
				currentValueLine = i
			}
		}
	}
}

func ReplaceTagValue(line string, value string) string {
	tr := regexp.MustCompile(`\bValue\s*:\s*.*`)
	return tr.ReplaceAllString(line, `Value: `+value)
}

func UpdateExistingSLSTags(tagLines []string, diff []*tags.TagDiff) {
	for i, line := range tagLines {
		key := strings.Split(strings.ReplaceAll(line, " ", ""), ":")[0]
		for _, tag := range diff {
			if key == tag.Key {
				lineWithoutValue := strings.Split(line, ":")[0]
				tagLines[i] = lineWithoutValue + ": " + tag.NewValue
			}
		}
	}
}

func computeResourcesLineRange(originLines []string, blocks []structure.IBlock, isCfn bool) structure.Lines {
	ret := structure.Lines{
		Start: -1,
		End:   -1,
	}
	minLine := math.Inf(0)
	maxLine := -1
	for _, block := range blocks {
		minLine = math.Min(minLine, float64(block.GetLines().Start))
		maxLine = int(math.Max(float64(maxLine), float64(block.GetLines().End)))
	}
	if !isCfn {
		functionsBlockStartLine := -1
		for i, line := range originLines {
			if strings.Contains(line, "functions:") {
				functionsBlockStartLine = i
				break
			}
		}
		minLine = math.Min(minLine, float64(functionsBlockStartLine))
	}
	ret.Start = int(minLine)
	ret.End = maxLine
	return ret
}

func getYAMLLines(rawBlock interface{}) []string {
	var textLines []string
	yamlBytes, err := yaml.Marshal(rawBlock)
	if err != nil {
		logger.Warning(fmt.Sprintf("failed to marshal resource to yaml: %s", err))
	}

	textLines = utils.GetLinesFromBytes(yamlBytes)

	return textLines
}

func FindTagsLinesYAML(textLines []string, tagsAttributeName string) (structure.Lines, bool) {
	tagsLines := structure.Lines{Start: -1, End: -1}
	var lineIndent string
	var tagsExist bool
	var tagsIndent = ""
	for i, line := range textLines {
		lineIndent = ExtractIndentationOfLine(line)
		switch {
		case strings.HasPrefix(strings.TrimSpace(line), tagsAttributeName+":"):
			tagsLines.Start = i
			tagsIndent = lineIndent
			tagsExist = true
		case lineIndent <= tagsIndent && (tagsLines.Start >= 0 || i == len(textLines)-1):
			tagsLines.End = findLastNonEmptyLine(textLines, i-1)
			return tagsLines, tagsExist
		case i == len(textLines)-1 && !tagsExist:
			return tagsLines, tagsExist
		}
	}
	if !tagsExist {
		tagsLines.Start = tagsLines.End
	} else if tagsLines.End == -1 {
		tagsLines.End = findLastNonEmptyLine(textLines, len(textLines)-1)
	}
	return tagsLines, tagsExist
}

func MapResourcesLineYAML(filePath string, resourceNames []string, resourcesStartToken string) (map[string]*structure.Lines, []string) {
	resourceToLines := make(map[string]*structure.Lines)
	skipResourcesByComment := make([]string, 0)
	for _, resourceName := range resourceNames {
		// initialize a map between resource name and its lines in file
		resourceToLines[resourceName] = &structure.Lines{Start: -1, End: -1}
	}
	// #nosec G304
	file, err := os.ReadFile(filePath)
	if err != nil {
		logger.Warning(fmt.Sprintf("failed to read file %s", filePath))
		return nil, skipResourcesByComment
	}

	readResources := false
	latestResourceName := ""
	fileLines := strings.Split(string(file), "\n")
	resourcesIndent := 0
	// iterate file line by line
	for i, line := range fileLines {
		cleanContent := strings.TrimSpace(line)
		if strings.HasPrefix(cleanContent, resourcesStartToken+":") {
			// There is no line above when the token is the first line of the file, which
			// is ordinary in a serverless.yml that opens with "functions:".
			if i > 0 && strings.ToUpper(strings.TrimSpace(fileLines[i-1])) == "#YOR:SKIPALL" {
				skipResourcesByComment = append(skipResourcesByComment, resourceNames...)
			}
			readResources = true
			resourcesIndent = countLeadingSpaces(line)
			continue
		}

		if readResources {
			if i > 0 {
				if strings.ToUpper(strings.TrimSpace(fileLines[i-1])) == "#YOR:SKIP" {

					skipResourcesByComment = append(skipResourcesByComment, strings.Trim(strings.TrimSpace(line), ":"))

				}
			}
			lineIndent := countLeadingSpaces(line)
			if lineIndent <= resourcesIndent && strings.TrimSpace(line) != "" && !strings.Contains(line, "#") {
				// No longer inside resources block, get the last line of the previous resource if exists
				//nolint:ineffassign
				readResources = false
				if latestResourceName != "" {
					resourceToLines[latestResourceName].End = findLastNonEmptyLine(fileLines, i-1)
				}
				break
			}
			for _, resName := range resourceNames {
				resNameRegex := regexp.MustCompile(fmt.Sprintf("^ {1,5}%v:", resName))
				if resNameRegex.Match([]byte(line)) {
					if latestResourceName != "" {
						// Complete previous function block
						resourceToLines[latestResourceName].End = findLastNonEmptyLine(fileLines, i-1)
					}
					latestResourceName = resName
					resourceToLines[latestResourceName].Start = i
					break
				}
			}
			if !strings.HasPrefix(line, " ") && strings.TrimSpace(line) != "" && readResources && latestResourceName != "" && !strings.HasPrefix(line, "#") {
				// This is no longer in the functions block, complete last function block
				resourceToLines[latestResourceName].End = findLastNonEmptyLine(fileLines, i-1)
				break
			}
		}
	}
	if latestResourceName != "" && resourceToLines[latestResourceName].End == -1 {
		// Handle last line of resource is last line of file
		resourceToLines[latestResourceName].End = findLastNonEmptyLine(fileLines, len(fileLines)-1)
	}
	return resourceToLines, skipResourcesByComment
}

func countLeadingSpaces(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

func findLastNonEmptyLine(fileLines []string, maxIndex int) int {
	for i := utils.MinInt(maxIndex, len(fileLines)-1); i >= 0; i-- {
		if strings.TrimSpace(fileLines[i]) != "" {
			return i
		}
	}
	return 0
}

func IndentLines(textLines []string, indent string, valueIndent int) []string {
	for i, originLine := range textLines {
		var blankSpaces string
		if valueIndent == 0 {
			blankSpaces = SingleIndent
		} else {
			blankSpaces = strings.Repeat(" ", valueIndent)
		}
		noLeadingWhitespace := strings.TrimLeft(originLine, "\t \n")
		if strings.Contains(originLine, "- Key") {
			textLines[i] = indent + noLeadingWhitespace
		} else {
			textLines[i] = indent + blankSpaces + noLeadingWhitespace
		}
	}

	return textLines
}

func ExtractIndentationOfLine(textLine string) string {
	indent := ""
	for _, c := range textLine {
		if c != ' ' && c != '-' {
			break
		}
		indent += " "
	}

	return indent
}
