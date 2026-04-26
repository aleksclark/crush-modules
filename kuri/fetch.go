package kuri

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"charm.land/fantasy"
)

const fetchDescription = `Fetches content from a URL using kuri-fetch and returns it in the specified format.

<when_to_use>
Use this tool when you need to:
- Fetch raw content from a URL without AI processing
- Get HTML, markdown, text, JSON, or links from a webpage
- Simple, fast content retrieval

DO NOT use this tool when you need to:
- Interact with a page (click, type, navigate) — use agentic_browser
- Extract specific information requiring analysis — use agentic_fetch or agentic_browser
</when_to_use>

<usage>
- Provide URL to fetch content from
- Specify desired output format (text, markdown, html, json, or links)
- Optional: provide file_path to control where the file is saved
- Optional: enable JavaScript execution for dynamic content
- Content is always saved to a file and the path is returned
</usage>

<features>
- Five output formats: text, markdown (default), html, json, links
- Optional JavaScript execution via embedded QuickJS engine
- SSRF protection (blocks private IPs)
- No browser required — pure HTTP fetch
</features>

<limitations>
- Cannot handle pages requiring login or cookies
- JavaScript execution is limited to inline scripts (no full browser)
- Some heavily JS-dependent sites may return incomplete content
</limitations>

<tips>
- Use "links" format to extract all URLs from a page
- Use "json" format for structured output with metadata
- Enable js for pages with inline script-rendered content
- For full browser interaction, use agentic_browser instead
- Provide file_path to choose where the file is saved; otherwise a name is derived from the URL
- Use the View tool to read the saved file
</tips>
`

// FetchParams defines the parameters for the kuri_fetch tool.
type FetchParams struct {
	URL      string `json:"url" jsonschema:"description=The URL to fetch content from,required"`
	Format   string `json:"format,omitempty" jsonschema:"description=Output format: text, markdown (default), html, json, or links,enum=text,enum=markdown,enum=html,enum=json,enum=links,default=markdown"`
	FilePath string `json:"file_path,omitempty" jsonschema:"description=Local file path to save the fetched content to. If omitted a filename is derived from the URL."`
	JS       bool   `json:"js,omitempty" jsonschema:"description=Execute inline JavaScript via QuickJS engine,default=false"`
	Timeout  int    `json:"timeout,omitempty" jsonschema:"description=Timeout in seconds (max 120),default=30"`
}

// NewFetchTool creates the kuri_fetch tool.
func NewFetchTool(cfg Config, workingDir string) fantasy.AgentTool {
	kuriFetchBin := cfg.KuriFetchPath
	if kuriFetchBin == "" {
		kuriFetchBin = "kuri-fetch"
	}

	return fantasy.NewAgentTool(
		FetchToolName,
		fetchDescription,
		func(ctx context.Context, params FetchParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.URL == "" {
				return fantasy.NewTextErrorResponse("url is required"), nil
			}

			if !strings.HasPrefix(params.URL, "http://") && !strings.HasPrefix(params.URL, "https://") {
				params.URL = "https://" + params.URL
			}

			format := params.Format
			if format == "" {
				format = "markdown"
			}

			timeout := params.Timeout
			if timeout <= 0 {
				timeout = 30
			}
			if timeout > 120 {
				timeout = 120
			}

			args := []string{params.URL, "--dump", format, "--quiet", "--no-color"}
			if params.JS {
				args = append(args, "--js")
			}

			cmdCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
			defer cancel()

			cmd := exec.CommandContext(cmdCtx, kuriFetchBin, args...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			if err := cmd.Run(); err != nil {
				if cmdCtx.Err() == context.DeadlineExceeded {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("kuri-fetch timed out after %ds", timeout)), nil
				}
				errMsg := stderr.String()
				if errMsg == "" {
					errMsg = err.Error()
				}
				return fantasy.NewTextErrorResponse(fmt.Sprintf("kuri-fetch error: %s", strings.TrimSpace(errMsg))), nil
			}

			content := stdout.String()
			if content == "" {
				return fantasy.NewTextResponse("(empty response)"), nil
			}

			filePath := params.FilePath
			if filePath == "" {
				filePath = filenameFromURL(params.URL, format)
			}

			return writeToFile(content, filePath, workingDir)
		},
	)
}

var safeFilenameRe = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

// filenameFromURL derives a reasonable filename from a URL and format.
func filenameFromURL(rawURL, format string) string {
	ext := formatToExt(format)

	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return "fetch-output" + ext
	}

	name := parsed.Host
	if p := strings.TrimSuffix(parsed.Path, "/"); p != "" && p != "/" {
		name += "_" + strings.ReplaceAll(strings.TrimPrefix(p, "/"), "/", "_")
	}

	name = safeFilenameRe.ReplaceAllString(name, "_")
	if len(name) > 120 {
		name = name[:120]
	}

	return name + ext
}

func formatToExt(format string) string {
	switch format {
	case "html":
		return ".html"
	case "json":
		return ".json"
	case "markdown":
		return ".md"
	case "links":
		return ".txt"
	default:
		return ".txt"
	}
}

// smartJoin joins two paths, treating the second as absolute if it is.
func smartJoin(base, path string) string {
	if smartIsAbs(path) {
		return path
	}
	return filepath.Join(base, path)
}

func smartIsAbs(path string) bool {
	if runtime.GOOS == "windows" {
		return filepath.IsAbs(path) || strings.HasPrefix(filepath.ToSlash(path), "/")
	}
	return filepath.IsAbs(path)
}

func writeToFile(content, filePath, workingDir string) (fantasy.ToolResponse, error) {
	absPath := smartJoin(workingDir, filePath)
	relPath, _ := filepath.Rel(workingDir, absPath)
	relPath = filepath.ToSlash(cmp.Or(relPath, absPath))

	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return fantasy.ToolResponse{}, fmt.Errorf("failed to create parent directories: %w", err)
	}

	f, err := os.Create(absPath)
	if err != nil {
		return fantasy.ToolResponse{}, fmt.Errorf("failed to create file: %w", err)
	}
	defer f.Close()

	n, err := io.WriteString(f, content)
	if err != nil {
		return fantasy.ToolResponse{}, fmt.Errorf("failed to write file: %w", err)
	}

	return fantasy.NewTextResponse(fmt.Sprintf("Successfully saved %d bytes to %s", n, relPath)), nil
}
