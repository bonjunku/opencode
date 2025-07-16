package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/opencode-ai/opencode/internal/config"
	"github.com/opencode-ai/opencode/internal/diff"
	"github.com/opencode-ai/opencode/internal/history"
	"github.com/opencode-ai/opencode/internal/logging"
	"github.com/opencode-ai/opencode/internal/lsp"
	"github.com/opencode-ai/opencode/internal/permission"
)

type WriteParams struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

type WritePermissionsParams struct {
	FilePath string `json:"file_path"`
	Diff     string `json:"diff"`
}

type writeTool struct {
	lspClients  map[string]*lsp.Client
	permissions permission.Service
	files       history.Service
}

type WriteResponseMetadata struct {
	Diff      string `json:"diff"`
	Additions int    `json:"additions"`
	Removals  int    `json:"removals"`
}

const (
	WriteToolName    = "write"
	writeDescription = `File writing tool that creates or updates files in the filesystem, allowing you to save or modify text content.

WHEN TO USE THIS TOOL:
- Use when you need to create a new file
- Helpful for updating existing files with modified content
- Perfect for saving generated code, configurations, or text data

HOW TO USE:
- Provide the path to the file you want to write
- Include the content to be written to the file
- The tool will create any necessary parent directories

FEATURES:
- Can create new files or overwrite existing ones
- Creates parent directories automatically if they don't exist
- Checks if the file has been modified since last read for safety
- Avoids unnecessary writes when content hasn't changed

LIMITATIONS:
- You should read a file before writing to it to avoid conflicts
- Cannot append to files (rewrites the entire file)


TIPS:
- Use the View tool first to examine existing files before modifying them
- Use the LS tool to verify the correct location when creating new files
- Combine with Glob and Grep tools to find and modify multiple files
- Always include descriptive comments when making changes to existing code`
)

func NewWriteTool(lspClients map[string]*lsp.Client, permissions permission.Service, files history.Service) BaseTool {
	return &writeTool{
		lspClients:  lspClients,
		permissions: permissions,
		files:       files,
	}
}

func (w *writeTool) Info() ToolInfo {
	return ToolInfo{
		Name:        WriteToolName,
		Description: writeDescription,
		Parameters: map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "The path to the file to write",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The content to write to the file",
			},
		},
		Required: []string{"file_path", "content"},
	}
}

func (w *writeTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params WriteParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %s", err)), nil
	}

	if params.FilePath == "" {
		// Generate a default filename based on content
		params.FilePath = generateDefaultFilename(params.Content)
	}

	if params.Content == "" {
		return NewTextErrorResponse("content is required"), nil
	}

	filePath := params.FilePath
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(config.WorkingDirectory(), filePath)
	}

	fileInfo, err := os.Stat(filePath)
	if err == nil {
		if fileInfo.IsDir() {
			return NewTextErrorResponse(fmt.Sprintf("Path is a directory, not a file: %s", filePath)), nil
		}

		modTime := fileInfo.ModTime()
		lastRead := getLastReadTime(filePath)
		if modTime.After(lastRead) {
			return NewTextErrorResponse(fmt.Sprintf("File %s has been modified since it was last read.\nLast modification: %s\nLast read: %s\n\nPlease read the file again before modifying it.",
				filePath, modTime.Format(time.RFC3339), lastRead.Format(time.RFC3339))), nil
		}

		oldContent, readErr := os.ReadFile(filePath)
		if readErr == nil && string(oldContent) == params.Content {
			return NewTextErrorResponse(fmt.Sprintf("File %s already contains the exact content. No changes made.", filePath)), nil
		}
	} else if !os.IsNotExist(err) {
		return ToolResponse{}, fmt.Errorf("error checking file: %w", err)
	}

	dir := filepath.Dir(filePath)
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return ToolResponse{}, fmt.Errorf("error creating directory: %w", err)
	}

	oldContent := ""
	if fileInfo != nil && !fileInfo.IsDir() {
		oldBytes, readErr := os.ReadFile(filePath)
		if readErr == nil {
			oldContent = string(oldBytes)
		}
	}

	sessionID, messageID := GetContextValues(ctx)
	if sessionID == "" || messageID == "" {
		return ToolResponse{}, fmt.Errorf("session_id and message_id are required")
	}

	diff, additions, removals := diff.GenerateDiff(
		oldContent,
		params.Content,
		filePath,
	)

	rootDir := config.WorkingDirectory()
	permissionPath := filepath.Dir(filePath)
	if strings.HasPrefix(filePath, rootDir) {
		permissionPath = rootDir
	}
	p := w.permissions.Request(
		permission.CreatePermissionRequest{
			SessionID:   sessionID,
			Path:        permissionPath,
			ToolName:    WriteToolName,
			Action:      "write",
			Description: fmt.Sprintf("Create file %s", filePath),
			Params: WritePermissionsParams{
				FilePath: filePath,
				Diff:     diff,
			},
		},
	)
	if !p {
		return ToolResponse{}, permission.ErrorPermissionDenied
	}

	err = os.WriteFile(filePath, []byte(params.Content), 0o644)
	if err != nil {
		return ToolResponse{}, fmt.Errorf("error writing file: %w", err)
	}

	// Check if file exists in history
	file, err := w.files.GetByPathAndSession(ctx, filePath, sessionID)
	if err != nil {
		_, err = w.files.Create(ctx, sessionID, filePath, oldContent)
		if err != nil {
			// Log error but don't fail the operation
			return ToolResponse{}, fmt.Errorf("error creating file history: %w", err)
		}
	}
	if file.Content != oldContent {
		// User Manually changed the content store an intermediate version
		_, err = w.files.CreateVersion(ctx, sessionID, filePath, oldContent)
		if err != nil {
			logging.Debug("Error creating file history version", "error", err)
		}
	}
	// Store the new version
	_, err = w.files.CreateVersion(ctx, sessionID, filePath, params.Content)
	if err != nil {
		logging.Debug("Error creating file history version", "error", err)
	}

	recordFileWrite(filePath)
	recordFileRead(filePath)
	waitForLspDiagnostics(ctx, filePath, w.lspClients)

	result := fmt.Sprintf("File successfully written: %s", filePath)
	result = fmt.Sprintf("<result>\n%s\n</result>", result)
	result += getDiagnostics(filePath, w.lspClients)
	return WithResponseMetadata(NewTextResponse(result),
		WriteResponseMetadata{
			Diff:      diff,
			Additions: additions,
			Removals:  removals,
		},
	), nil
}

// generateDefaultFilename creates a filename based on content analysis
func generateDefaultFilename(content string) string {
	// Trim whitespace and get first few lines for analysis
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) == 0 {
		return "output.txt"
	}

	// Check for common file patterns
	firstLine := strings.TrimSpace(lines[0])
	contentLower := strings.ToLower(content)

	// Programming languages detection
	if strings.HasPrefix(firstLine, "#!/usr/bin/env python") || strings.HasPrefix(firstLine, "#!/usr/bin/python") ||
		strings.Contains(contentLower, "def ") || strings.Contains(contentLower, "import ") ||
		strings.Contains(contentLower, "from ") || regexp.MustCompile(`print\s*\(`).MatchString(contentLower) {
		return "script.py"
	}

	if strings.Contains(contentLower, "#include") || strings.Contains(contentLower, "int main") ||
		strings.Contains(contentLower, "std::") || strings.Contains(contentLower, "cout") {
		return "program.cpp"
	}

	if strings.Contains(contentLower, "#include") && strings.Contains(contentLower, "printf") {
		return "program.c"
	}

	if strings.Contains(contentLower, "public class") || strings.Contains(contentLower, "public static void main") ||
		strings.Contains(contentLower, "System.out.println") {
		return "Program.java"
	}

	if strings.Contains(contentLower, "function") || strings.Contains(contentLower, "console.log") ||
		strings.Contains(contentLower, "const ") || strings.Contains(contentLower, "let ") ||
		strings.Contains(contentLower, "var ") || strings.Contains(contentLower, "=>") {
		return "script.js"
	}

	if strings.Contains(contentLower, "func ") || strings.Contains(contentLower, "package main") ||
		strings.Contains(contentLower, "import \"") || strings.Contains(contentLower, "fmt.") {
		return "program.go"
	}

	if strings.Contains(contentLower, "fn ") || strings.Contains(contentLower, "println!") ||
		strings.Contains(contentLower, "use std::") {
		return "program.rs"
	}

	if strings.Contains(contentLower, "<!DOCTYPE") || strings.Contains(contentLower, "<html") ||
		strings.Contains(contentLower, "<body") || strings.Contains(contentLower, "<div") {
		return "index.html"
	}

	if strings.Contains(contentLower, "<?xml") || strings.Contains(contentLower, "<xml") {
		return "document.xml"
	}

	if strings.Contains(contentLower, "{") && strings.Contains(contentLower, "}") &&
		(strings.Contains(contentLower, "\"") || strings.Contains(contentLower, ":")) {
		return "data.json"
	}

	// Shell scripts
	if strings.HasPrefix(firstLine, "#!/bin/bash") || strings.HasPrefix(firstLine, "#!/bin/sh") ||
		strings.Contains(contentLower, "echo ") || strings.Contains(contentLower, "if [") {
		return "script.sh"
	}

	// SQL
	if strings.Contains(contentLower, "select ") || strings.Contains(contentLower, "create table") ||
		strings.Contains(contentLower, "insert into") || strings.Contains(contentLower, "update ") {
		return "query.sql"
	}

	// Markdown
	if strings.HasPrefix(firstLine, "#") || strings.Contains(contentLower, "##") ||
		strings.Contains(contentLower, "```") || strings.Contains(contentLower, "**") {
		return "document.md"
	}

	// Configuration files
	if strings.Contains(contentLower, "[") && strings.Contains(contentLower, "]") &&
		strings.Contains(contentLower, "=") {
		return "config.ini"
	}

	// YAML
	if strings.Contains(contentLower, "---") || regexp.MustCompile(`^\s*\w+:\s*`).MatchString(firstLine) {
		return "config.yaml"
	}

	// CSV
	if strings.Contains(contentLower, ",") && strings.Count(firstLine, ",") > 1 {
		return "data.csv"
	}

	// Default to text file
	return "output.txt"
}
