package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

const (
	// LogDirEnv is the environment variable to override the default log directory
	LogDirEnv   = "CONFAB_LOG_DIR"
	logDirName  = ".confab/logs"
	logFileName = "confab.log"
	maxSizeMB   = 1     // 1MB per file
	maxAgeDays  = 14    // Keep 2 weeks
	maxBackups  = 20    // Max old log files (safety limit)
	compressOld = true  // Compress rotated logs
)

// Level represents the log level
type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
)

func (l Level) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// Logger manages logging to file and optionally stderr
type Logger struct {
	file       io.WriteCloser
	logger     *log.Logger
	logPath    string
	level      Level
	mu         sync.Mutex
	alsoStderr bool   // Also write to stderr
	sessionCtx string // Session context prefix (e.g., "session=abc123")
}

var (
	instance *Logger
	once     sync.Once
)

// ResetForTesting resets the singleton state for tests.
// This allows tests to re-initialize the logger with different settings.
// Most tests don't need this - the auto-discard behavior handles isolation.
// Use this when testing logger initialization itself or verifying log output.
func ResetForTesting() {
	if instance != nil && instance.file != nil {
		instance.file.Close()
	}
	instance = nil
	once = sync.Once{}
}

// Init initializes the logger (creates log directory and file).
//
// Test isolation: When running under `go test` (detected via testing.Testing()),
// if CONFAB_LOG_DIR is not explicitly set, logs are discarded to avoid polluting
// the real log file. Tests that need to verify log output should set CONFAB_LOG_DIR
// to a temp directory.
func Init() error {
	var err error
	once.Do(func() {
		logDir := os.Getenv(LogDirEnv)

		// Auto-discard logs in tests unless explicitly configured
		if logDir == "" && testing.Testing() {
			instance = &Logger{
				logger: log.New(io.Discard, "", 0),
				level:  INFO,
			}
			return
		}

		if logDir == "" {
			home, homeErr := os.UserHomeDir()
			if homeErr != nil {
				err = fmt.Errorf("failed to get home directory: %w", homeErr)
				return
			}
			logDir = filepath.Join(home, logDirName)
		}

		if mkdirErr := os.MkdirAll(logDir, 0755); mkdirErr != nil {
			err = fmt.Errorf("failed to create log directory: %w", mkdirErr)
			return
		}

		logPath := filepath.Join(logDir, logFileName)

		// Use lumberjack for automatic log rotation
		rotator := &lumberjack.Logger{
			Filename:   logPath,
			MaxSize:    maxSizeMB,   // megabytes
			MaxAge:     maxAgeDays,  // days
			MaxBackups: maxBackups,  // number of old files
			Compress:   compressOld, // compress old files
			LocalTime:  true,        // use local time for filenames
		}

		instance = &Logger{
			file:       rotator,
			logger:     log.New(rotator, "", 0), // We'll format manually
			logPath:    logPath,
			level:      INFO,
			alsoStderr: false,
		}
	})
	return err
}

// Get returns the logger instance (initializes if needed)
func Get() *Logger {
	if instance == nil {
		if err := Init(); err != nil {
			// Fallback to stderr-only logger
			instance = &Logger{
				logger:     log.New(os.Stderr, "", 0),
				level:      INFO,
				alsoStderr: true,
			}
		}
	}
	return instance
}

// Close closes the log file
func Close() error {
	if instance != nil && instance.file != nil {
		return instance.file.Close()
	}
	return nil
}

// SetLevel sets the minimum log level
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// SetAlsoStderr sets whether to also write to stderr
func (l *Logger) SetAlsoStderr(enabled bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.alsoStderr = enabled
}

// SetSessionContext sets a session context that will be included in all log lines.
// Pass empty string to clear the context.
func (l *Logger) SetSessionContext(ctx string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sessionCtx = ctx
}

// SetSession sets session IDs that will be included in all log lines.
// externalID is the Claude Code session ID, sessionID is the Confab backend session ID.
// Either can be empty if not yet known.
func (l *Logger) SetSession(externalID, sessionID string) {
	var ctx string
	if externalID != "" && sessionID != "" {
		ctx = fmt.Sprintf("[ext=%s sess=%s]", shortID(externalID), shortID(sessionID))
	} else if externalID != "" {
		ctx = fmt.Sprintf("[ext=%s]", shortID(externalID))
	} else if sessionID != "" {
		ctx = fmt.Sprintf("[sess=%s]", shortID(sessionID))
	}
	l.SetSessionContext(ctx)
}

// shortID returns first 8 chars of an ID for brevity in logs
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// log writes a log message at the specified level
func (l *Logger) log(level Level, format string, args ...interface{}) {
	if level < l.level {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	message := fmt.Sprintf(format, args...)

	var logLine string
	if l.sessionCtx != "" {
		logLine = fmt.Sprintf("[%s] %s %s: %s\n", timestamp, l.sessionCtx, level, message)
	} else {
		logLine = fmt.Sprintf("[%s] %s: %s\n", timestamp, level, message)
	}

	// Write to log file
	if l.logger != nil {
		l.logger.Print(logLine)
	}

	// Also write to stderr if enabled
	if l.alsoStderr {
		fmt.Fprint(os.Stderr, logLine)
	}
}

// Debug logs a debug message
func (l *Logger) Debug(format string, args ...interface{}) {
	l.log(DEBUG, format, args...)
}

// Info logs an info message
func (l *Logger) Info(format string, args ...interface{}) {
	l.log(INFO, format, args...)
}

// Warn logs a warning message
func (l *Logger) Warn(format string, args ...interface{}) {
	l.log(WARN, format, args...)
}

// Error logs an error message
func (l *Logger) Error(format string, args ...interface{}) {
	l.log(ERROR, format, args...)
}

// logAndPrint logs to file at the specified level AND prints a user-friendly message to stderr
func (l *Logger) logAndPrint(level Level, format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)

	// Always log to file with full formatting
	l.log(level, format, args...)

	// Print user-friendly message to stderr (no timestamp/level prefix)
	fmt.Fprintln(os.Stderr, message)
}

// ErrorPrint logs an error to file AND prints to stderr for user visibility
func (l *Logger) ErrorPrint(format string, args ...interface{}) {
	l.logAndPrint(ERROR, format, args...)
}

// Package-level convenience functions

// Debug logs a debug message (file only, not shown to user)
func Debug(format string, args ...interface{}) {
	Get().Debug(format, args...)
}

// Info logs an info message (file only, not shown to user)
func Info(format string, args ...interface{}) {
	Get().Info(format, args...)
}

// Warn logs a warning (file only, not shown to user)
func Warn(format string, args ...interface{}) {
	Get().Warn(format, args...)
}

// Error logs an error (file only, not shown to user)
func Error(format string, args ...interface{}) {
	Get().Error(format, args...)
}

// ErrorPrint logs an error AND prints to stderr for user visibility
func ErrorPrint(format string, args ...interface{}) {
	Get().ErrorPrint(format, args...)
}

// SetSession sets session IDs that will be included in all log lines
func SetSession(externalID, sessionID string) {
	Get().SetSession(externalID, sessionID)
}
