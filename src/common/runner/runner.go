package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"plugin"
	"reflect"
	"strconv"
	"strings"
	"sync"

	cfnStructure "github.com/bridgecrewio/yor/src/cloudformation/structure"
	"github.com/bridgecrewio/yor/src/common"
	"github.com/bridgecrewio/yor/src/common/clioptions"
	"github.com/bridgecrewio/yor/src/common/logger"
	"github.com/bridgecrewio/yor/src/common/reports"
	"github.com/bridgecrewio/yor/src/common/tagging"
	"github.com/bridgecrewio/yor/src/common/tagging/external"
	"github.com/bridgecrewio/yor/src/common/tagging/simple"
	"github.com/bridgecrewio/yor/src/common/tagging/tags"
	taggingUtils "github.com/bridgecrewio/yor/src/common/tagging/utils"
	"github.com/bridgecrewio/yor/src/common/utils"
	slsStructure "github.com/bridgecrewio/yor/src/serverless/structure"
	tfStructure "github.com/bridgecrewio/yor/src/terraform/structure"
)

type Runner struct {
	TagGroups            []tagging.ITagGroup
	parsers              []common.IParser
	ChangeAccumulator    *reports.TagChangeAccumulator
	reportingService     *reports.ReportService
	dir                  string
	skipDirs             []string
	skippedTags          []string
	configFilePath       string
	skippedResourceTypes []string
	skippedResources     []string
	workersNum           int
	dryRun               bool
	nonRecursive         bool
}

const WorkersNumEnvKey = "YOR_WORKER_NUM"
const DefaultWorkersNum = "10"

func (r *Runner) Init(commands *clioptions.TagOptions) error {
	dir := commands.Directory
	extraTags, extraTagGroups, err := loadExternalResources(commands.CustomTagging)
	if err != nil {
		logger.Warning(fmt.Sprintf("failed to load extenal tags from plugins due to error: %s", err))
	}
	for _, group := range commands.TagGroups {
		tagGroup := taggingUtils.TagGroupsByName(taggingUtils.TagGroupName(group))
		r.TagGroups = append(r.TagGroups, tagGroup)
	}
	r.TagGroups = append(r.TagGroups, extraTagGroups...)
	if commands.ConfigFile == "" {
		logger.Info("Did not get an external config file")
	}
	for _, tagGroup := range r.TagGroups {
		tagGroup.InitTagGroup(dir, commands.SkipTags, commands.Tag, tagging.WithTagPrefix(commands.TagPrefix))
		if simpleTagGroup, ok := tagGroup.(*simple.TagGroup); ok {
			simpleTagGroup.SetTags(extraTags)
		} else if externalTagGroup, ok := tagGroup.(*external.TagGroup); ok && commands.ConfigFile != "" {
			externalTagGroup.InitExternalTagGroups(commands.ConfigFile, commands.UseCodeOwners)
		}
	}
	processedParsers := map[string]struct{}{}
	for _, p := range commands.Parsers {
		if _, exists := processedParsers[p]; exists {
			continue
		}
		switch p {
		case "Terraform":
			r.parsers = append(r.parsers, &tfStructure.TerraformParser{})
		case "CloudFormation":
			r.parsers = append(r.parsers, &cfnStructure.CloudformationParser{})
		case "Serverless":
			r.parsers = append(r.parsers, &slsStructure.ServerlessParser{})
		default:
			logger.Warning(fmt.Sprintf("ignoring unknown parser %#v", err))
		}
		processedParsers[p] = struct{}{}
	}
	options := map[string]string{
		"tag-local-modules": strconv.FormatBool(commands.TagLocalModules)}
	for _, parser := range r.parsers {
		parser.Init(dir, options)
	}

	r.ChangeAccumulator = reports.TagChangeAccumulatorInstance
	r.reportingService = reports.ReportServiceInst
	r.dir = commands.Directory
	r.skippedTags = commands.SkipTags
	commands.SkipDirs = append(commands.SkipDirs, ".git")
	r.skipDirs = commands.SkipDirs
	r.configFilePath = commands.ConfigFile
	r.dryRun = commands.DryRun
	r.nonRecursive = commands.NonRecursive
	if utils.InSlice(r.skipDirs, r.dir) {
		logger.Warning(fmt.Sprintf("Selected dir, %s, is skipped - expect an empty result", r.dir))
	}
	r.skippedResourceTypes = commands.SkipResourceTypes
	r.skippedResources = commands.SkipResources
	var convErr error
	r.workersNum, convErr = strconv.Atoi(utils.GetEnv(WorkersNumEnvKey, DefaultWorkersNum))
	// A value of 0 or less parses cleanly but starts no workers at all, and the send on
	// the unbuffered file channel then blocks for ever: "all goroutines are asleep -
	// deadlock!". Reject it the same way as a value that is not a number.
	if convErr != nil || r.workersNum < 1 {
		logger.Error(fmt.Sprintf("Got an invalid value for %v, %q. It must be a positive whole number. If you didn't mean to leverage this option, please unset %v",
			WorkersNumEnvKey, os.Getenv(WorkersNumEnvKey), WorkersNumEnvKey))
	}
	return nil
}

func (r *Runner) worker(fileChan chan string, wg *sync.WaitGroup) {
	for file := range fileChan {
		r.TagFile(file)
		wg.Done()
	}
}

func (r *Runner) TagDirectory() (*reports.ReportService, error) {
	var files []string
	err := filepath.Walk(r.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// filepath.Walk passes a nil info alongside the error, so carrying on here
			// dereferenced nil - a path that cannot be stat'ed (permissions, a path too
			// long for the platform, a file removed mid-scan) crashed the whole run.
			logger.Warning(fmt.Sprintf("Failed to scan %s: %s", path, err))
			return nil
		}
		if r.nonRecursive && info.IsDir() && path != r.dir {
			return filepath.SkipDir
		}
		if !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		// Passing exactly two arguments makes the logger treat the second as an error
		// type and drop the message entirely, so this exited 1 with no output at all.
		logger.Error(fmt.Sprintf("Failed to run Walk() on root dir %s: %s", r.dir, err))
	}

	var wg sync.WaitGroup
	wg.Add(len(files))
	fileChan := make(chan string)

	for i := 0; i < r.workersNum; i++ {
		go r.worker(fileChan, &wg)
	}

	for _, file := range files {
		fileChan <- file
	}
	close(fileChan)
	wg.Wait()

	for _, parser := range r.parsers {
		parser.Close()
	}

	return r.reportingService, nil
}

func (r *Runner) isSkippedResourceType(resourceType string) bool {
	for _, skippedResourceType := range r.skippedResourceTypes {
		if resourceType == skippedResourceType {
			return true
		}
	}
	return false
}

// isSkippedResource reports whether the resource was named in --skip-resources. Skips
// coming from #yor:skip comments are carried on the block itself, because they apply to
// one resource in one file and matching them by id across files silenced identically
// named resources elsewhere.
func (r *Runner) isSkippedResource(resource string) bool {
	for _, skippedResource := range r.skippedResources {
		if resource == skippedResource {
			return true
		}
	}

	return false
}

func (r *Runner) TagFile(file string) {
	for _, parser := range r.parsers {
		if r.isFileSkipped(parser, file) {
			logger.Debug(fmt.Sprintf("%v parser Skipping %v", parser.Name(), file))
			continue
		}
		logger.Info(fmt.Sprintf("Tagging %v\n", file))
		blocks, err := parser.ParseFile(file)
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to parse file %v with parser %v", file, reflect.TypeOf(parser)))
			continue
		}
		isFileTaggable := false
		for _, block := range blocks {
			if r.isSkippedResourceType(block.GetResourceType()) {
				continue
			}
			if block.IsSkippedByComment() {
				logger.Debug(fmt.Sprintf("Skipping %v:%v due to a yor skip comment", file, block.GetResourceID()))
				continue
			}
			if r.isSkippedResource(block.GetResourceID()) {
				continue
			}
			if block.IsBlockTaggable() {
				logger.Debug(fmt.Sprintf("Tagging %v:%v", file, block.GetResourceID()))
				isFileTaggable = true
				for _, tagGroup := range r.TagGroups {
					err := tagGroup.CreateTagsForBlock(block)
					if err != nil {
						logger.Warning(fmt.Sprintf("Failed to tag %v in %v due to %v", block.GetResourceID(), block.GetFilePath(), err.Error()))
						continue
					}
				}
			} else {
				logger.Debug(fmt.Sprintf("Block %v:%v is not taggable, skipping", file, block.GetResourceID()))
			}
			r.ChangeAccumulator.AccumulateChanges(block)
		}
		if isFileTaggable && !r.dryRun {
			err = parser.WriteFile(file, blocks, file)
			if err != nil {
				logger.Warning(fmt.Sprintf("Failed writing tags to file %s, because %v", file, err))
			}
		}
	}
}

func loadExternalResources(externalPaths []string) ([]tags.ITag, []tagging.ITagGroup, error) {
	var extraTags []tags.ITag
	var extraTagGroups []tagging.ITagGroup
	var plugins []string

	for _, path := range externalPaths {
		// find all .so files under the given externalPaths
		err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if strings.HasSuffix(info.Name(), ".so") {
				plugins = append(plugins, path)
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}

		for _, pluginPath := range plugins {
			plug, err := plugin.Open(pluginPath)
			if err != nil {
				return nil, nil, err
			}

			iPtrs, err := extractExternalResources(plug, "ExtraTags")
			if err != nil {
				return nil, nil, err
			}
			for _, iTag := range iPtrs {
				tag, ok := iTag.(tags.ITag)
				if !ok {
					return nil, nil, fmt.Errorf("unexpected type from module symbol")
				}
				extraTags = append(extraTags, tag)
			}
			iPtrs, err = extractExternalResources(plug, "ExtraTagGroups")
			if err != nil {
				return nil, nil, err
			}
			for _, iTagGroup := range iPtrs {
				if tagGroup, ok := iTagGroup.(tagging.ITagGroup); ok {
					extraTagGroups = append(extraTagGroups, tagGroup)
				} else {
					return nil, nil, fmt.Errorf("unexpected type from module symbol ExtraTagGroups")
				}
			}
		}
	}

	return extraTags, extraTagGroups, nil
}

func extractExternalResources(plug *plugin.Plugin, symbol string) ([]interface{}, error) {
	symExtraTags, err := plug.Lookup(symbol)
	if err != nil {
		return nil, nil
	}
	logger.Info("Found values for the symbol:", symbol)
	// convert to its actual type, *[]interface{}
	var iArrPtr *[]interface{}
	iArrPtr, ok := symExtraTags.(*[]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected type from module symbol")
	}

	return *iArrPtr, nil
}

func (r *Runner) isFileSkipped(p common.IParser, file string) bool {
	relPath, _ := filepath.Rel(r.dir, file)
	for _, sp := range r.skipDirs {
		if strings.HasPrefix(filepath.Join(r.dir, relPath), sp) {
			return true
		}
	}

	matchingSuffix := false
	for _, suffix := range p.GetSupportedFileExtensions() {
		if strings.HasSuffix(file, suffix) {
			matchingSuffix = true
		}
	}
	if !matchingSuffix {
		return true
	}
	for _, pattern := range p.GetSkippedDirs() {
		if strings.Contains(file, pattern) {
			return true
		}
	}
	return !p.ValidFile(file)
}
