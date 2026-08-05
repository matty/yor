package json

import (
	"bytes"
	"encoding/json"
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
)

// WriteJSONFile updates the content of `readFilePath` with updated tags from `blocks` and writes it to `writeFilePath`
func WriteJSONFile(readFilePath string, blocks []structure.IBlock, writeFilePath string, fileBracketsPairs map[int]BracketPair) error {

	// #nosec G304
	originFileSrc, err := os.ReadFile(readFilePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s because %s", readFilePath, err)
	}
	originFileStr := string(originFileSrc)

	newStringsByStartChar := make(map[int]string) // map between start char index and the string that should be written in that index
	Start2EndCharMap := make(map[int]int)         // map start index to end index
	for _, resourceBlock := range blocks {
		if resourceBlock.IsBlockTaggable() {
			tagsDiff := resourceBlock.CalculateTagsDiff()
			if len(tagsDiff.Added) == 0 && len(tagsDiff.Updated) == 0 {
				// if resource was not changed during the run, continue
				continue
			}

			resourceBrackets := FindScopeInJSON(originFileStr, resourceBlock.GetResourceID(), fileBracketsPairs, &structure.Lines{Start: -1, End: -1})
			Start2EndCharMap[resourceBrackets.Open.CharIndex] = resourceBrackets.Close.CharIndex
			newResourceLines := AddTagsToResourceStr(originFileStr, resourceBlock, fileBracketsPairs)
			newStringsByStartChar[resourceBrackets.Open.CharIndex] = newResourceLines
		}
	}

	// write changes
	textToWrite := originFileStr
	if len(newStringsByStartChar) > 0 {
		// sort start chars in ascending order
		startChars := make([]int, 0, len(newStringsByStartChar))
		for c := range newStringsByStartChar {
			startChars = append(startChars, c)
		}
		sort.Ints(startChars)

		textToWrite = ""
		lastReplacedIndex := 0
		for _, cIndex := range startChars {
			// write text until next changed string and append new string
			textToWrite += originFileStr[lastReplacedIndex:cIndex] + newStringsByStartChar[cIndex]
			// set the pointer of the string continuation to be the end index of the replaced part.
			lastReplacedIndex = Start2EndCharMap[cIndex] + 1
		}
		textToWrite += originFileStr[lastReplacedIndex:]
	}

	err = os.WriteFile(writeFilePath, []byte(textToWrite), 0600)
	return err
}

// AddTagsToResourceStr gets the entire context as a string, and returns a string of a resource with the updated tags
func AddTagsToResourceStr(fullOriginStr string, resourceBlock structure.IBlock, fileBracketsPairs map[int]BracketPair) string {
	logger.Debug(fmt.Sprintf("setting tags to resource %s in path %s", resourceBlock.GetResourceID(), resourceBlock.GetFilePath()))
	diff := resourceBlock.CalculateTagsDiff()
	// extract the resource's brackets scope and get the origin str for that resource
	resourceBrackets := FindScopeInJSON(fullOriginStr, resourceBlock.GetResourceID(), fileBracketsPairs, &structure.Lines{Start: -1, End: -1})
	resourceStr := fullOriginStr[resourceBrackets.Open.CharIndex : resourceBrackets.Close.CharIndex+1]

	tagsAttributeName := resourceBlock.GetTagsAttributeName()
	indexOfTags := findJSONKeyIndex(resourceStr, tagsAttributeName) // get the start index of the tags key in the resource string
	if indexOfTags >= 0 {
		// extract the tags' brackets scope and get the origin str for them
		tagBrackets := FindScopeInJSON(fullOriginStr, tagsAttributeName, fileBracketsPairs, &structure.Lines{Start: resourceBrackets.Open.Line, End: resourceBrackets.Close.Line})
		tagsStr := fullOriginStr[tagBrackets.Open.CharIndex : tagBrackets.Close.CharIndex+1]
		tagsLinesList := strings.Split(tagsStr, "\n")
		UpdateExistingTags(tagsLinesList, diff.Updated)

		//	now find the indentation of the first tags entry by searching an indent between "[" and first "{". If there is a newline, restart the indent.
		tagBlockIndent := findIndent(tagsStr, '{', 0) // find the indent of each tag block " { "
		// Index of the "{" opening the first tag object. -1 means the array holds no
		// tag objects at all (an empty `"Tags": []`), which is handled separately below.
		firstTagBraceIndex := strings.Index(tagsStr, "{")
		// The gap between that "{" and the first key tells us whether the key sits on
		// its own line.
		firstTagStr := ""
		if firstTagBraceIndex >= 0 {
			if quoteOffset := strings.Index(tagsStr[firstTagBraceIndex+1:], `"`); quoteOffset >= 0 {
				firstTagStr = tagsStr[firstTagBraceIndex+1 : firstTagBraceIndex+1+quoteOffset]
			}
		}
		tagEntryIndent := findIndent(tagsStr, '"', firstTagBraceIndex) // find the indent of the key and value entry
		compact := false
		switch {
		case firstTagBraceIndex < 0:
			// The tags attribute exists but is an empty array, so there is nothing to
			// preserve and no existing entry to take the indentation from. Rewrite the
			// whole array from the added tags, indented like the line the array opens on.
			return replaceEmptyTagsArray(fullOriginStr, resourceStr, diff.Added, resourceBrackets, tagBrackets)
		case strings.Contains(firstTagStr, "\n"):
			// If the tag string has a newline, it means the indent needs to be re-evaluated. Example for this use case:
			// "Tags": [
			//   {
			//     "Key": "some-key",
			//     "Value": "some-val"
			//   }
			// ]
			indentDiff := len(tagEntryIndent) - len(tagBlockIndent)
			tagBlockIndent = trimIndentBy(tagBlockIndent, indentDiff)
			tagEntryIndent = trimIndentBy(tagEntryIndent, indentDiff)
		case len(tagsLinesList) == 1:
			// multi tags in one line
			compact = true
		default:
			// Otherwise, need to take the indent of the "{" character. This case handles:
			// "Tags": [
			//   { "Key": "some-key", "Value": "some-val" }
			// ]
			tagBlockIndent = trimIndentBy(tagBlockIndent, 1)

		}

		finalTagsStr := ""
		switch {
		case len(diff.Added) == 0:
			// Nothing to append: every tag key is already present and only values
			// changed, which UpdateExistingTags has already rewritten in tagsLinesList.
			// json.MarshalIndent of an empty set yields the single line "null"/"[]", so
			// the append paths below have no tag lines to splice in.
			finalTagsStr = strings.Join(tagsLinesList, "\n")
		default:
			// unmarshal updated tags with the indent matching origin file. This will create the tags with the `[]` wrapping which will be discarded later
			strAddedTags, err := json.MarshalIndent(diff.Added, tagBlockIndent, strings.TrimPrefix(tagEntryIndent, tagBlockIndent))
			if err != nil {
				logger.Warning(fmt.Sprintf("failed to unmarshal tags %s with indent '%s' because of error: %s", diff.Added, tagBlockIndent, err))
				return resourceStr
			}

			if compact {
				dst := &bytes.Buffer{}
				if err := json.Compact(dst, strAddedTags); err != nil {
					logger.Warning(fmt.Sprintf("failed to build tags %s with err: %s", strAddedTags, err))
					return resourceStr
				}
				tagsLine := tagsLinesList[0]

				finalTagsStr = tagsLine[:len(tagsLine)-1] + "," + dst.String()[1:]
			} else {
				netNewTagLines := strings.Split(string(strAddedTags), "\n")
				if len(netNewTagLines) < 3 {
					logger.Warning(fmt.Sprintf("unexpected marshalled tags layout for resource %s, leaving it untouched", resourceBlock.GetResourceID()))
					return resourceStr
				}
				finalTagsStr = strings.Join(tagsLinesList[:len(tagsLinesList)-1], "\n") + ",\n" +
					strings.Join(netNewTagLines[1:len(netNewTagLines)-1], "\n") + "\n" +
					tagsLinesList[len(tagsLinesList)-1]
			}
		}
		tagsStartRelativeToResource := tagBrackets.Open.CharIndex - resourceBrackets.Open.CharIndex
		tagsEndRelativeToResource := tagBrackets.Close.CharIndex - resourceBrackets.Open.CharIndex
		if tagsStartRelativeToResource < 0 || tagsEndRelativeToResource < tagsStartRelativeToResource ||
			tagsEndRelativeToResource+1 > len(resourceStr) {
			// The tags scope was not resolved inside this resource, so splicing by these
			// offsets would either panic or graft the tags into the wrong place.
			logger.Warning(fmt.Sprintf("could not locate the tags scope inside resource %s, leaving it untouched", resourceBlock.GetResourceID()))
			return resourceStr
		}

		// set the resource string with the updated and indented tags
		resourceStr = resourceStr[:tagsStartRelativeToResource] + finalTagsStr + resourceStr[tagsEndRelativeToResource+1:]
	} else {
		// step 1 - extract the parent of the tags attribute from the new resource (not from the file)
		jsonResourceStr := getJSONStr(resourceBlock.GetRawBlock()) // encode raw block to json
		identifiersToAdd := make([]string, 0)
		parentIdentifier := tagsAttributeName

		// step 2 - find the parent identifier in the origin resource. If not found continue to look for identifiers until reaching the resource name
		indexOfParent := -1
		for indexOfParent < 0 && parentIdentifier != resourceBlock.GetResourceID() {
			identifiersToAdd = append(identifiersToAdd, parentIdentifier)
			parentIdentifier = FindParentIdentifier(jsonResourceStr, parentIdentifier)
			if parentIdentifier == "" {
				identifiersToAdd = append(identifiersToAdd, resourceBlock.GetResourceID())
				break
			}
			indexOfParent = findJSONKeyIndex(resourceStr, parentIdentifier)
		}

		if len(identifiersToAdd) == 0 {
			// Only possible when the tags attribute name is the resource id itself, in
			// which case there is no parent chain to build.
			logger.Warning(fmt.Sprintf("could not resolve where to add tags for resource %s, leaving it untouched", resourceBlock.GetResourceID()))
			return resourceStr
		}

		// step 3 - find indent from last parent scope start to it's first child
		topIdentifierScope := FindScopeInJSON(fullOriginStr, identifiersToAdd[len(identifiersToAdd)-1], fileBracketsPairs, &structure.Lines{Start: resourceBrackets.Open.Line, End: resourceBrackets.Close.Line})
		var indent string
		if indexOfParent == -1 {
			// Need to extract the indent of "Type", not of the Resource
			indent = findIndent(resourceStr, '"', 0)
		} else {
			indent = findIndent(fullOriginStr, '"', topIdentifierScope.Open.CharIndex)
		}
		// step 4 - add the missing data

		// create a map of data to add
		entriesToAdd := make(map[string]interface{})
		iterator := entriesToAdd
		for i := len(identifiersToAdd) - 1; i >= 0; i-- {
			if identifiersToAdd[i] == resourceBlock.GetResourceID() {
				continue
			}
			if i > 0 {
				iterator[identifiersToAdd[i]] = make(map[string]interface{})
				iterator = iterator[identifiersToAdd[i]].(map[string]interface{})
			} else {
				iterator[identifiersToAdd[i]] = diff.Added
			}
		}
		indentStr := "  "
		// marshal the map using the extracted indentation
		jsonToAdd, err := json.MarshalIndent(entriesToAdd, indent, indentStr)
		if err != nil {
			logger.Warning(fmt.Sprintf("failed to unmarshal tags %s with indent '%s' because of error: %s", entriesToAdd, indent, err))
			return resourceStr
		}
		textToAdd := string(jsonToAdd)
		if len(textToAdd) < 2 {
			logger.Warning(fmt.Sprintf("unexpected marshalled tags layout for resource %s, leaving it untouched", resourceBlock.GetResourceID()))
			return resourceStr
		}

		// remove first and last chars, which are '{' and '}' - we already have the top level map and don't need it
		textToAdd = textToAdd[1 : len(textToAdd)-1]

		// fix indentation after removing the top level map
		lines := strings.Split(textToAdd, "\n")
		editedLines := make([]string, 0)
		for _, l := range lines {
			for c := range l {
				if !utils.IsCharWhitespace(l[c]) {
					newL := strings.Replace(l, indentStr, "", 1)
					editedLines = append(editedLines, newL)
					break
				}
			}
		}

		// add comma after tags
		textToAdd = "\n" + strings.Join(editedLines, "\n") + ","

		// adding the tags as the first element
		if indexOfParent == -1 {
			// Properties attribute does not exist on this resource, need to add it
			topIdentifierScope.Open.CharIndex = resourceBrackets.Open.CharIndex
			topIdentifierScope.Close.CharIndex = resourceBrackets.Open.CharIndex
		}
		resourceStr = resourceStr[:(topIdentifierScope.Open.CharIndex+1)-resourceBrackets.Open.CharIndex] + textToAdd + resourceStr[(topIdentifierScope.Open.CharIndex+1)-resourceBrackets.Open.CharIndex:]
	}

	return resourceStr
}

// replaceEmptyTagsArray rewrites an empty `"Tags": []` array with the tags that need
// to be added. There is no existing entry to copy the layout from, so the array is
// re-marshalled using the indentation of the line the array opens on.
func replaceEmptyTagsArray(fullOriginStr string, resourceStr string, added []tags.ITag, resourceBrackets BracketPair, tagBrackets BracketPair) string {
	if len(added) == 0 {
		return resourceStr
	}
	start := tagBrackets.Open.CharIndex - resourceBrackets.Open.CharIndex
	end := tagBrackets.Close.CharIndex - resourceBrackets.Open.CharIndex
	if start < 0 || end < start || end+1 > len(resourceStr) {
		logger.Warning("could not locate the tags array inside the resource, leaving it untouched")
		return resourceStr
	}
	baseIndent := lineIndentAt(fullOriginStr, tagBrackets.Open.CharIndex)
	marshalled, err := json.MarshalIndent(added, baseIndent, "  ")
	if err != nil {
		logger.Warning(fmt.Sprintf("failed to marshal tags %s because of error: %s", added, err))
		return resourceStr
	}

	return resourceStr[:start] + string(marshalled) + resourceStr[end+1:]
}

// lineIndentAt returns the leading whitespace of the line containing `index`.
func lineIndentAt(str string, index int) string {
	if index < 0 || index > len(str) {
		return ""
	}
	lineStart := strings.LastIndex(str[:index], "\n") + 1
	indent := ""
	for i := lineStart; i < len(str); i++ {
		if str[i] != ' ' && str[i] != '\t' {
			break
		}
		indent += string(str[i])
	}

	return indent
}

// UpdateExistingTags rewrites the value of every tag in tagsLinesList that appears in
// diff, in place.
//
// "Key" and "Value" may appear in either order within a tag object, so each object is
// resolved as a unit. The previous implementation carried a pending Value line across
// object boundaries and only cleared it when a Key matched, so for the usual
// Key-then-Value layout an updated tag wrote its new value onto the *preceding* tag's
// Value line and left its own value stale.
func UpdateExistingTags(tagsLinesList []string, diff []*tags.TagDiff) {
	newValueByKey := make(map[string]string, len(diff))
	for _, tagDiff := range diff {
		newValueByKey[tagDiff.Key] = tagDiff.NewValue
	}

	keyLine, valueLine := -1, -1
	tagKey := ""
	applyPendingTag := func() {
		if keyLine >= 0 && valueLine >= 0 {
			if newValue, ok := newValueByKey[tagKey]; ok {
				tagsLinesList[valueLine] = ReplaceTagValue(tagsLinesList[valueLine], newValue)
			}
		}
		keyLine, valueLine, tagKey = -1, -1, ""
	}

	for i, tagLine := range tagsLinesList {
		if strings.Contains(tagLine, "{") {
			// A new tag object starts here, so anything pending belongs to the previous one.
			applyPendingTag()
		}
		if strings.Contains(tagLine, `"Key"`) {
			keyLine = i
			tagKey = extractJSONKeyName(tagLine)
		}
		if strings.Contains(tagLine, `"Value"`) {
			valueLine = i
		}
		if strings.Contains(tagLine, "}") {
			applyPendingTag()
		}
	}
	applyPendingTag()
}

var jsonTagKeyRegex = regexp.MustCompile(`"Key"\s*:\s*"([^"]*)"`)

// extractJSONKeyName pulls the tag name out of a `"Key": "<name>"` line. Matching the
// name exactly avoids updating a tag whose key merely contains another key as a substring.
func extractJSONKeyName(tagLine string) string {
	match := jsonTagKeyRegex.FindStringSubmatch(tagLine)
	if match == nil {
		return ""
	}

	return match[1]
}

func ReplaceTagValue(tagLine string, valueToSet string) string {
	tr := regexp.MustCompile(`"Value"\s*:\s*".*?"`)
	return tr.ReplaceAllString(tagLine, `"Value": "`+valueToSet+`"`)
}

// findIndent finds the indentation in a string `str` from starting char index until `charToStop` is identified
// if a newline is encountered, restart the indentation to ""
// A negative startIndex, or a `charToStop` that never appears, yields "" rather than
// reading past either end of the string.
func findIndent(str string, charToStop byte, startIndex int) string {
	indent := ""
	if startIndex < 0 {
		startIndex = 0
	}
	for charIndex := startIndex; charIndex < len(str); charIndex++ {
		currChar := str[charIndex]
		if currChar == charToStop {
			return indent
		}
		if utils.IsCharWhitespace(currChar) {
			if currChar == '\n' {
				indent = ""
			} else {
				indent += string(currChar)
			}
		} else {
			indent = ""
		}
	}

	return indent
}

// trimIndentBy drops `n` characters from the end of an indentation string, clamping
// instead of panicking when `n` is negative or longer than the string.
func trimIndentBy(indent string, n int) string {
	if n <= 0 {
		return indent
	}
	if n >= len(indent) {
		return ""
	}
	return indent[:len(indent)-n]
}

// getJSONStr marshals an interface into json and return a string of that json
func getJSONStr(jsonData interface{}) string {
	jsonBytes, err := json.Marshal(jsonData)
	if err != nil {
		logger.Warning(fmt.Sprintf("failed to marshal resource to json: %s", err))
		return ""
	}

	return string(jsonBytes)
}

// MapResourcesLineJSON maps the lines of all resources in a file and return it with the brackets mapping
func MapResourcesLineJSON(filePath string, resourceNames []string) (map[string]*structure.Lines, map[int]BracketPair) {
	resourceToLines := make(map[string]*structure.Lines)
	// #nosec G304
	file, err := os.ReadFile(filePath)
	if err != nil {
		logger.Warning(fmt.Sprintf("failed to read file %s", filePath))
		return nil, nil
	}

	fileStr := string(file)
	bracketsInFile := MapBracketsInString(fileStr)
	bracketPairs := GetBracketsPairs(bracketsInFile)

	for _, resourceName := range resourceNames {
		matchingBrackets := FindScopeInJSON(fileStr, resourceName, bracketPairs, &structure.Lines{Start: -1, End: -1})
		resourceToLines[resourceName] = &structure.Lines{Start: matchingBrackets.Open.Line, End: matchingBrackets.Close.Line}
	}

	return resourceToLines, bracketPairs
}

// MapBracketsInString finds all brackets in a string, ignoring any that appear
// inside a JSON string literal. Counting brackets inside string values throws the
// open/close pairing off, which both misplaces tags and can leave a close bracket
// with no matching open one - a template with a brace in a Description, an inline
// IAM policy or a UserData script was enough to do it.
func MapBracketsInString(str string) []Brackets {
	allBrackets := make([]Brackets, 0)
	lineCounter := 1
	inString := false
	escaped := false
	for cIndex, c := range str {
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			case c == '\n':
				// A raw newline cannot appear inside a JSON string, so treat it as the
				// end of one. Without this an unbalanced quote would swallow the rest
				// of the file and every bracket after it.
				inString = false
				lineCounter++
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			allBrackets = append(allBrackets, Brackets{Type: OpenBrackets, Shape: CurlyBrackets, Line: lineCounter, CharIndex: cIndex})
		case '}':
			allBrackets = append(allBrackets, Brackets{Type: CloseBrackets, Shape: CurlyBrackets, Line: lineCounter, CharIndex: cIndex})
		case '[':
			allBrackets = append(allBrackets, Brackets{Type: OpenBrackets, Shape: SquareBrackets, Line: lineCounter, CharIndex: cIndex})
		case ']':
			allBrackets = append(allBrackets, Brackets{Type: CloseBrackets, Shape: SquareBrackets, Line: lineCounter, CharIndex: cIndex})
		case '\n':
			lineCounter++
		}
	}

	return allBrackets
}

// GetBracketsPairs given a list of all brackets of pair, map all the pairs correctly and return them ordered by the open char index
func GetBracketsPairs(bracketsInString []Brackets) map[int]BracketPair {
	startCharToBrackets := make(map[int]BracketPair)
	bracketShape2BracketsStacks := make(map[BracketShape][]Brackets)

	for _, bracket := range bracketsInString {
		stack, ok := bracketShape2BracketsStacks[bracket.Shape]
		if bracket.Type == OpenBrackets {
			if !ok {
				stack = make([]Brackets, 0)
			}
			stack = append(stack, bracket)
			bracketShape2BracketsStacks[bracket.Shape] = stack
		} else {
			// `ok` only tells us this bracket shape was seen before, not that there is
			// still an unmatched open bracket to pair with, so the stack can be empty here.
			if !ok || len(stack) == 0 {
				logger.Warning("malformed json file", "SILENT")
				return startCharToBrackets
			}
			openBracket := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			bracketShape2BracketsStacks[bracket.Shape] = stack
			startCharToBrackets[openBracket.CharIndex] = BracketPair{Open: openBracket, Close: bracket}
		}
	}

	return startCharToBrackets
}

// FindScopeInJSON finds the index of a key in json string and return the start and end brackets of the key scope
func FindScopeInJSON(str string, key string, bracketsPairs map[int]BracketPair, linesRange *structure.Lines) BracketPair {
	foundBracketPair := BracketPair{}
	indexOfKey := getKeyIndex(str, key, linesRange)

	nextIndex := math.MaxInt32
	for index, bracketPair := range bracketsPairs {
		if index > indexOfKey {
			if bracketPair.Open.CharIndex < nextIndex {
				nextIndex = bracketPair.Open.CharIndex
				foundBracketPair = bracketPair
			}
		}
	}

	return foundBracketPair
}

// FindOuterScopeInJSON finds the index of a key in json string and return the start and end brackets wrapping the key
func FindOuterScopeInJSON(str string, key string, bracketsPairs map[int]BracketPair, linesRange *structure.Lines) BracketPair {
	foundBracketPair := BracketPair{}
	indexOfKey := getKeyIndex(str, key, linesRange)

	nextIndex := -1
	for index, bracketPair := range bracketsPairs {
		if index < indexOfKey {
			if bracketPair.Open.CharIndex > nextIndex {
				nextIndex = bracketPair.Open.CharIndex
				foundBracketPair = bracketPair
			}
		}
	}

	return foundBracketPair
}

func getKeyIndex(str string, key string, linesRange *structure.Lines) int {
	var indexOfKey int
	if linesRange.Start != -1 {
		fileLines := strings.Split(str, "\n")
		// The line numbers recorded by MapBracketsInString are 1-based while fileLines
		// is 0-based. That mismatch shifts which lines get searched and is deliberately
		// left alone here (fixing it changes the line numbers yor reports and tags
		// with); these bounds only stop it from indexing off the end of the slice - a
		// JSON file with no trailing newline was enough to do that.
		start := clampLineIndex(linesRange.Start, len(fileLines))
		end := clampLineIndex(linesRange.End, len(fileLines))
		beforeRange := strings.Join(fileLines[:start], "\n")
		rangeLinesStr := ""
		switch {
		case start < end:
			rangeLinesStr = strings.Join(fileLines[start:end], "\n")
		case start < len(fileLines):
			rangeLinesStr = fileLines[start]
		}
		indexOfKey = findJSONKeyIndex(rangeLinesStr, key)
		indexOfKey = len(beforeRange) + indexOfKey + 1 // add 1 for the lost newline
	} else {
		indexOfKey = findJSONKeyIndex(str, key)
	}

	return indexOfKey
}

func clampLineIndex(line int, lineCount int) int {
	if line < 0 {
		return 0
	}
	if line > lineCount {
		return lineCount
	}
	return line
}

// FindWrappingBrackets given a brackets pair, find the pair that wraps them
func FindWrappingBrackets(allBracketPairs map[int]BracketPair, innerBracketPair BracketPair) BracketPair {
	wrappingPair := -1
	for i, bracketsPair := range allBracketPairs {
		if bracketsPair.Open.CharIndex < innerBracketPair.Open.CharIndex && bracketsPair.Close.CharIndex > innerBracketPair.Close.CharIndex {
			if wrappingPair == -1 || (bracketsPair.Open.CharIndex > allBracketPairs[wrappingPair].Open.CharIndex && bracketsPair.Close.CharIndex < allBracketPairs[wrappingPair].Close.CharIndex) {
				wrappingPair = i
			}
		}
	}

	return allBracketPairs[wrappingPair]
}

// FindParentIdentifier finds the identifier of the parent of a given child.
// for example, str = {parent: {child: [...] }} and childIdentifier="child", return "parent"
func FindParentIdentifier(str string, childIdentifier string) string {
	// create mapping of all brackets in resource
	bracketsInResourceJSON := MapBracketsInString(str)
	bracketsPairsInResourceJSON := GetBracketsPairs(bracketsInResourceJSON)

	// get tags brackets
	childScope := FindScopeInJSON(str, childIdentifier, bracketsPairsInResourceJSON, &structure.Lines{Start: -1, End: -1})
	wrappingBracketsScope := FindWrappingBrackets(bracketsPairsInResourceJSON, childScope)
	if childScope.Open.CharIndex == 0 && childScope.Close.CharIndex == 0 {
		wrappingBracketsScope = FindOuterScopeInJSON(str, childIdentifier, bracketsPairsInResourceJSON, &structure.Lines{Start: -1, End: -1})
	}
	// find the brackets that wrap the "tags"
	// extract the name of the tags' parent (for example, in CFN it will be "Properties")
	r := regexp.MustCompile("\"")
	if wrappingBracketsScope.Open.CharIndex == 0 {
		return ""
	}
	quoteMarksIndexes := r.FindAllStringIndex(str[:wrappingBracketsScope.Open.CharIndex], -1)
	if len(quoteMarksIndexes) < 2 {
		// No quoted identifier precedes the wrapping brackets, so there is no parent name to read.
		return ""
	}
	indexOfLastQuoteMark := quoteMarksIndexes[len(quoteMarksIndexes)-1][0]
	indexOfSecondToLastQuoteMark := quoteMarksIndexes[len(quoteMarksIndexes)-2][0]
	parentIdentifier := str[indexOfSecondToLastQuoteMark+1 : indexOfLastQuoteMark]

	return parentIdentifier
}

// findJSONKeyIndex finds the index of an entry in a JSON by wrapping it with "<key>":
func findJSONKeyIndex(str string, key string) int {
	r, _ := regexp.Compile("\"" + strings.ReplaceAll(key, "\"", "\\\"") + `"\s*:`) // support a case of one or more spaces before colon
	indexPair := r.FindStringIndex(str)
	if len(indexPair) == 0 {
		return -1
	}

	return indexPair[0]
}
