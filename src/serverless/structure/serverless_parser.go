package structure

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bridgecrewio/goformation/v5/intrinsics"
	"github.com/bridgecrewio/yor/src/common"
	"github.com/bridgecrewio/yor/src/common/logger"
	"github.com/bridgecrewio/yor/src/common/structure"
	"github.com/bridgecrewio/yor/src/common/tagging/tags"
	"github.com/bridgecrewio/yor/src/common/types"
	"github.com/bridgecrewio/yor/src/common/utils"
	yamlUtils "github.com/bridgecrewio/yor/src/common/yaml"
)

const FunctionTagsAttributeName = "tags"
const FunctionsSectionName = "functions"
const FunctionType = "function"

type ServerlessParser struct {
	YamlParser           types.YamlParser
	skippedByCommentList []string
}

var slsParseLock sync.Mutex

func (p *ServerlessParser) Name() string {
	return "Serverless"
}

func (p *ServerlessParser) Init(rootDir string, _ map[string]string) {
	p.YamlParser.RootDir = rootDir
}

func (p *ServerlessParser) Close() {}

func (p *ServerlessParser) GetSkippedDirs() []string {
	return []string{}
}

func (p *ServerlessParser) GetSupportedFileExtensions() []string {
	return []string{common.YamlFileType.Extension, common.YmlFileType.Extension}
}

func serverlessParse(file string) (*structure.Template, error) {
	var template *structure.Template
	var err error
	defer func() {
		if e := recover(); e != nil {
			logger.Warning(fmt.Sprintf("Failed to parser serverless yaml at %v due to: %v", file, e))
			err = fmt.Errorf("failed to parse sls file %v: %v", file, e)
		}
	}()
	slsParseLock.Lock()
	template, err = Open(file)
	slsParseLock.Unlock()
	return template, err
}

func (p *ServerlessParser) ValidFile(file string) bool {
	if _, err := serverlessParse(file); err != nil {
		return false
	}
	return true
}

func (p *ServerlessParser) ParseFile(filePath string) ([]structure.IBlock, error) {
	var skipResourcesByComment []string
	parsedBlocks := make([]structure.IBlock, 0)
	fileFormat := utils.GetFileFormat(filePath)
	fileName := filepath.Base(filePath)
	if !(fileName == fmt.Sprintf("serverless.%s", fileFormat) || fileName == fmt.Sprintf("config.%s", fileFormat)) {
		return nil, nil
	}
	// #nosec G304 - file is from user
	template, err := serverlessParse(filePath)
	if err != nil || template == nil || template.Functions == nil {
		if err != nil {
			logger.Warning(fmt.Sprintf("There was an error processing the serverless template: %s", err))
		}
		if err == nil {
			err = fmt.Errorf("failed to parse file %v", filePath)
		}
		return nil, err
	}
	if template.Functions == nil && template.Resources.Resources == nil {
		return parsedBlocks, nil
	}

	// cfnStackTagsResource := p.template.Provider.CFNTags
	resourceNames := make([]string, 0)
	var resourceNamesToLines map[string]*structure.Lines
	for funcName := range template.Functions {
		resourceNames = append(resourceNames, funcName)
	}
	switch utils.GetFileFormat(filePath) {
	case common.YmlFileType.FileFormat, common.YamlFileType.FileFormat:
		resourceNamesToLines, skipResourcesByComment = yamlUtils.MapResourcesLineYAML(filePath, resourceNames, FunctionsSectionName)
		p.skippedByCommentList = append(p.skippedByCommentList, skipResourcesByComment...)
	default:
		return nil, fmt.Errorf("unsupported file type %s", utils.GetFileFormat(filePath))
	}
	// math.MaxInt8 is 127, so any file whose functions all start after line 127 used to
	// report 127 as the start of the functions block.
	minResourceLine := math.MaxInt
	maxResourceLine := 0
	for _, funcName := range resourceNames {
		var existingTags []tags.ITag
		var slsBlock *ServerlessBlock
		tagsLines := structure.Lines{Start: -1, End: -1}
		slsFunction := template.Functions[funcName]
		lines, ok := resourceNamesToLines[funcName]
		if !ok || lines == nil {
			// MapResourcesLineYAML returns a nil map when the file cannot be read.
			logger.Warning(fmt.Sprintf("could not map function %s in file %s to lines, skipping it", funcName, filePath))
			continue
		}
		if lines.Start < minResourceLine {
			minResourceLine = lines.Start
		}
		if lines.End > maxResourceLine {
			maxResourceLine = lines.End
		}
		if slsFunction.Tags != nil {
			tagsLines = p.getTagsLines(filePath, lines)
			for tagKey, tagValue := range slsFunction.Tags {
				existingTags = append(existingTags, &tags.Tag{Key: tagKey, Value: fmt.Sprintf("%v", tagValue)})
			}
		}

		slsBlock = &ServerlessBlock{
			Block: structure.Block{
				FilePath:          filePath,
				ExitingTags:       existingTags,
				RawBlock:          slsFunction,
				IsTaggable:        true,
				TagsAttributeName: FunctionTagsAttributeName,
				Lines:             *lines,
				TagLines:          tagsLines,
				Name:              funcName,
				Type:              FunctionType,
			},
		}

		parsedBlocks = append(parsedBlocks, slsBlock)
		p.YamlParser.FileToResourcesLines.Store(filePath, structure.Lines{Start: minResourceLine, End: maxResourceLine})

	}
	return parsedBlocks, nil
}
func (p *ServerlessParser) GetSkipResourcesByComment() []string {
	return p.skippedByCommentList
}

func (p *ServerlessParser) WriteFile(readFilePath string, blocks []structure.IBlock, writeFilePath string) error {
	for _, block := range blocks {
		block := block.(*ServerlessBlock)
		block.UpdateTags()
	}
	tempFilePath, err := utils.CreateClosedTempFile(filepath.Dir(readFilePath), "temp.*.yaml")
	if err != nil {
		return err
	}
	defer func() {
		if removeErr := os.Remove(tempFilePath); removeErr != nil {
			logger.Warning(fmt.Sprintf("failed to remove temp file %s: %s", tempFilePath, removeErr))
		}
	}()

	err = yamlUtils.WriteYAMLFile(readFilePath, blocks, tempFilePath, FunctionTagsAttributeName, FunctionsSectionName)
	if err != nil {
		return err
	}
	_, err = p.ParseFile(tempFilePath)
	if err != nil {
		return fmt.Errorf("editing file %v resulted in a malformed template, please open a github issue with the relevant details", readFilePath)
	}
	return yamlUtils.WriteYAMLFile(readFilePath, blocks, writeFilePath, FunctionTagsAttributeName, FunctionsSectionName)
}

func (p *ServerlessParser) getTagsLines(filePath string, resourceLinesRange *structure.Lines) structure.Lines {
	nonFoundLines := structure.Lines{Start: -1, End: -1}
	fileFormat := utils.GetFileFormat(filePath)
	tagsLines := structure.Lines{Start: -1, End: -1}
	lineCounter := 0
	switch fileFormat {
	case common.YamlFileType.FileFormat, common.YmlFileType.FileFormat:
		scanner, closeFile, _ := utils.GetFileScanner(filePath, &nonFoundLines)
		if scanner == nil {
			return nonFoundLines
		}
		defer closeFile()
		// iterate file line by line
		tagsIndentSize := 0
		for scanner.Scan() {
			line := scanner.Text()
			lineIndent := len(yamlUtils.ExtractIndentationOfLine(line))
			if lineCounter < resourceLinesRange.Start+1 {
				lineCounter++
				continue
			}
			if lineCounter > resourceLinesRange.End || (tagsIndentSize > 0 && lineIndent <= tagsIndentSize) {
				tagsLines.End = lineCounter - 1
				break
			}
			if strings.TrimSpace(line) == FunctionTagsAttributeName+":" {
				tagsIndentSize = len(yamlUtils.ExtractIndentationOfLine(line))
				tagsLines.Start = lineCounter
				lineCounter++
				continue
			}
			lineCounter++
		}
	}
	if tagsLines.Start >= 0 && tagsLines.End == -1 {
		tagsLines.End = lineCounter - 1
	}
	return tagsLines
}

// Open and parse a Serverless template from file.
// Works with YAML formatted templates.
func Open(filename string) (*structure.Template, error) {
	// #nosec G304
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	return openYaml(data)
}

func openYaml(input []byte) (*structure.Template, error) {
	intrinsified, err := intrinsics.ProcessYAML(input, nil)
	if err != nil {
		return nil, err
	}
	template := &structure.Template{}
	if err := json.Unmarshal(intrinsified, template); err != nil {
		return nil, err
	}

	return template, nil
}
