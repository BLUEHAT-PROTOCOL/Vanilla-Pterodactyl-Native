// Package util provides shared helpers: logging, error envelopes, fs guards.
package util

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Logger writes timestamped lines to stderr and optionally a file.
type Logger struct {
	file  *os.File
	debug bool
}

// NewLogger creates a logger writing to stderr and path (best effort).
func NewLogger(path string, debug bool) *Logger {
	l := &Logger{debug: debug}
	if path != "" {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			l.file = f
		}
	}
	return l
}

func (l *Logger) write(level, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("[%s] %s: %s\n", time.Now().UTC().Format(time.RFC3339), level, msg)
	_ = l.output(line)
}

func (l *Logger) output(line string) error {
	_, _ = fmt.Fprint(os.Stderr, line)
	if l.file != nil {
		_, err := l.file.WriteString(line)
		return err
	}
	return nil
}

// Info logs an informational message.
func (l *Logger) Info(format string, args ...interface{}) { l.write("INFO", format, args...) }

// Warn logs a warning.
func (l *Logger) Warn(format string, args ...interface{}) { l.write("WARN", format, args...) }

// Error logs an error.
func (l *Logger) Error(format string, args ...interface{}) { l.write("ERROR", format, args...) }

// Debug logs when debug enabled.
func (l *Logger) Debug(format string, args ...interface{}) {
	if l.debug {
		l.write("DEBUG", format, args...)
	}
}

// APIError is the wings-compatible error envelope.
type APIError struct {
	Code    string `json:"code"`
	Status  string `json:"status"`
	Detail  string `json:"detail"`
	httpStt int
}

// Error implements error.
func (e *APIError) Error() string { return e.Detail }

// NewErr builds an APIError.
func NewErr(status int, code, detail string) *APIError {
	return &APIError{Code: code, Status: fmt.Sprintf("%d", status), Detail: detail, httpStt: status}
}

// ErrNotFound — unknown server or resource.
func ErrNotFound(what string) *APIError {
	return NewErr(http.StatusNotFound, "NotFoundException", what+" not found")
}

// ErrBadRequest — malformed request.
func ErrBadRequest(detail string) *APIError {
	return NewErr(http.StatusBadRequest, "BadRequestException", detail)
}

// ErrServerSuspended — suspended server action.
func ErrServerSuspended() *APIError {
	return NewErr(http.StatusBadRequest, "ServerSuspendedException", "server is suspended")
}

// ErrInternal — unexpected failure.
func ErrInternal(detail string) *APIError {
	return NewErr(http.StatusInternalServerError, "InternalApiErrorException", detail)
}

// ErrPowerConflict — power action in progress.
func ErrPowerConflict(detail string) *APIError {
	return NewErr(http.StatusConflict, "PowerActionConflict", detail)
}

// WriteJSON writes a JSON response with status.
func WriteJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// WriteError writes the error envelope.
func WriteError(w http.ResponseWriter, err error) {
	if ae, ok := err.(*APIError); ok {
		WriteJSON(w, ae.httpStt, map[string]interface{}{"errors": []interface{}{ae}})
		return
	}
	WriteJSON(w, http.StatusInternalServerError, map[string]interface{}{
		"errors": []interface{}{NewErr(http.StatusInternalServerError, "InternalApiErrorException", err.Error())},
	})
}

// TruncateLine caps a console line length.
func TruncateLine(s string, max int) string {
	s = strings.ReplaceAll(s, "\r", "")
	if len(s) > max {
		return s[:max]
	}
	return s
}
