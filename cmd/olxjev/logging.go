package main

import (
    "fmt"
    "io"
    "os"
    "time"

    "github.com/rs/zerolog"
)

// logFormats are -log-format's values: auto is console on a terminal and
// JSON lines otherwise (a container, a pipe, a log collector).
var logFormats = []string{"auto", "console", "json"}

// newLogger is the process's logger on w, at debug level with -debug.
func newLogger(w io.Writer, format string, debug bool) (zerolog.Logger, error) {
    switch format {
    case "auto":
        if isTerminal(w) {
            w = zerolog.ConsoleWriter{Out: w, TimeFormat: time.DateTime}
        }
    case "console":
        w = zerolog.ConsoleWriter{Out: w, TimeFormat: time.DateTime, NoColor: !isTerminal(w)}
    case "json":
    default:
        return zerolog.Nop(), fmt.Errorf("unknown -log-format %q (auto, console or json)", format)
    }
    level := zerolog.InfoLevel
    if debug {
        level = zerolog.DebugLevel
    }
    return zerolog.New(w).Level(level).With().Timestamp().Logger(), nil
}

// isTerminal reports whether w is a character device, such as a terminal.
func isTerminal(w io.Writer) bool {
    f, ok := w.(*os.File)
    if !ok {
        return false
    }
    fi, err := f.Stat()
    return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
