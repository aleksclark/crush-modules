package kuri

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
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
- Optional: enable JavaScript execution for dynamic content
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
</tips>
`

// FetchParams defines the parameters for the kuri_fetch tool.
type FetchParams struct {
	URL     string `json:"url" jsonschema:"description=The URL to fetch content from,required"`
	Format  string `json:"format,omitempty" jsonschema:"description=Output format: text, markdown (default), html, json, or links,enum=text,enum=markdown,enum=html,enum=json,enum=links,default=markdown"`
	JS      bool   `json:"js,omitempty" jsonschema:"description=Execute inline JavaScript via QuickJS engine,default=false"`
	Timeout int    `json:"timeout,omitempty" jsonschema:"description=Timeout in seconds (max 120),default=30"`
}

// NewFetchTool creates the kuri_fetch tool.
func NewFetchTool(cfg Config) fantasy.AgentTool {
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
			if len(content) > maxFetchResponseSize {
				content = content[:maxFetchResponseSize] + "\n\n[Content truncated at 100KB]"
			}

			if content == "" {
				return fantasy.NewTextResponse("(empty response)"), nil
			}

			return fantasy.NewTextResponse(content), nil
		},
	)
}

const maxFetchResponseSize = 100 * 1024
