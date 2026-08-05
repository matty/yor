package logger

import (
	"fmt"
	"log"
	"os"
	"strings"
)

type loggingService struct {
	logLevel LogLevel
	stdout   *os.File
	stderr   *os.File
}

type LogLevel int
type ErrorType int

const (
	DEBUG LogLevel = iota
	INFO
	WARNING
	ERROR
)

const (
	SILENT ErrorType = iota
)

var strLogLevels = map[LogLevel]string{
	DEBUG:   "DEBUG",
	INFO:    "INFO",
	WARNING: "WARNING",
	ERROR:   "ERROR",
}

var strErrorTypes = map[string]ErrorType{
	"SILENT": SILENT,
}

var Logger loggingService

func init() {
	log.SetFlags(log.Ldate | log.Ltime)
	Logger = loggingService{logLevel: WARNING, stdout: os.Stdout, stderr: os.Stderr}

	val, ok := os.LookupEnv("LOG_LEVEL")
	if ok {
		Logger.SetLogLevel(val)
	}
}

func (e *loggingService) log(logLevel LogLevel, args ...string) {
	if logLevel >= e.logLevel {
		var strArgs string
		if len(args) == 2 {
			strArgs = strings.Join([]string{args[0]}, " ")

		} else {
			strArgs = strings.Join(args, " ")
		}
		strArgs = fmt.Sprintf("[%s] ", strLogLevels[logLevel]) + strArgs
		switch logLevel {
		case DEBUG, INFO, WARNING:
			log.Println(strArgs)
		case ERROR:
			if len(args) == 2 {
				errorType := args[1]
				if _, ok := strErrorTypes[errorType]; ok {
					log.Println(strArgs)
				}
			} else {
				log.Println(strArgs)
			}
			os.Exit(1)
		}
	}
}

func Debug(args ...string) {
	Logger.log(DEBUG, args...)
}

func Info(args ...string) {
	Logger.log(INFO, args...)
}

func Warning(args ...string) {
	Logger.log(WARNING, args...)
}

func Error(args ...string) {
	Logger.log(ERROR, args...)
}

func (e *loggingService) SetLogLevel(inputLogLevel string) {
	logLevel := WARNING
	switch strings.ToUpper(inputLogLevel) {
	case "DEBUG":
		logLevel = DEBUG
	case "INFO":
		logLevel = INFO
	case "WARNING":
		logLevel = WARNING
	case "ERROR":
		logLevel = ERROR
	default:
		log.Println("Illegal log level received, defaulting to WARNING")
	}

	e.logLevel = logLevel
}

// MuteOutputBlock used to live here. It reassigned the process-wide os.Stdout and
// os.Stderr and flipped Logger.disabled for the duration of a callback, all without
// synchronising against the worker goroutines that log concurrently - a data race that
// also sent their output to the null device.
//
// Its single caller wrapped a module download, which was once "terraform get" shelling
// out and writing to stdout directly. That was replaced by a go-getter Client with no
// progress listener, which writes nothing to stdout, so there is no longer any output to
// suppress: the log level already governs the two logger calls in that block.
