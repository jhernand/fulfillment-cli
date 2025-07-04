/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package terminal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/innabox/fulfillment-cli/internal/templating"
)

// ConsoleBuilder contains the data and logic needed to create a console. Don't create objects of this type directly,
// use the NewConsole function instead.
type ConsoleBuilder struct {
	logger *slog.Logger
}

// Console is helps writing messages to the console. Don't create objects of this type directly, use the NewConsole
// function instead.
type Console struct {
	logger *slog.Logger
}

// NewConsole creates a builder that can the be used to create a template engine.
func NewConsole() *ConsoleBuilder {
	return &ConsoleBuilder{}
}

// SetLogger sets the logger that the console will use to write messages to the log. This is mandatory.
func (b *ConsoleBuilder) SetLogger(value *slog.Logger) *ConsoleBuilder {
	b.logger = value
	return b
}

// Build uses the configuration stored in the builder to create a new console.
func (b *ConsoleBuilder) Build() (result *Console, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}

	// Create and populate the object:
	result = &Console{
		logger: b.logger,
	}
	return
}

func (c *Console) Printf(ctx context.Context, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	c.logger.DebugContext(
		ctx,
		"Console printf",
		slog.String("format", format),
		slog.Any("args", args),
		slog.Any("text", text),
	)
	_, err := os.Stdout.WriteString(text)
	if err != nil {
		c.logger.ErrorContext(
			ctx,
			"Failed to write text",
			slog.String("text", text),
			slog.Any("error", err),
		)
	}
}

func (c *Console) Render(ctx context.Context, engine *templating.Engine, template string, data any) {
	buffer := &bytes.Buffer{}
	err := engine.Execute(buffer, template, data)
	if err != nil {
		c.logger.ErrorContext(
			ctx,
			"Failed to execute template",
			slog.String("template", template),
			slog.Any("error", err),
		)
	}
	text := buffer.String()
	lines := strings.Split(text, "\n")
	previousEmpty := true
	for _, line := range lines {
		currentEmpty := len(line) == 0
		if currentEmpty {
			if !previousEmpty {
				fmt.Fprintf(os.Stdout, "\n")
				previousEmpty = true
			}
		} else {
			fmt.Fprintf(os.Stdout, "%s\n", line)
			previousEmpty = false
		}
	}
}
