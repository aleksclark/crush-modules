package kuri

import (
	"context"
	"fmt"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/plugin"
)

const browserDescription = `Launches an autonomous browser agent that can navigate, interact with, and extract information from web pages using a real Chrome browser.

<when_to_use>
Use this tool when you need to:
- Interact with web pages (click buttons, fill forms, navigate)
- Browse pages that require JavaScript rendering
- Extract information from dynamic/interactive web applications
- Perform multi-step web workflows (login, search, extract)
- Audit web page security (cookies, headers, JWT tokens)
- Take screenshots of web pages

DO NOT use this tool when:
- You just need raw content from a static page (use kuri_fetch or fetch instead — faster and cheaper)
- You want to search the web (use agentic_fetch instead)
- The page content is available via simple HTTP fetch
</when_to_use>

<usage>
- Provide a prompt describing what you want to accomplish
- Optionally provide a starting URL
- The browser agent will autonomously navigate, interact, and report results
</usage>

<features>
- Full Chrome browser with JavaScript support
- Click, type, select, scroll interactions via accessibility tree references
- Page snapshots with semantic element references (@eN notation)
- Screenshot capture
- Security auditing (cookies, headers, JWT scanning)
- Multi-tab support
- Stealth mode for anti-bot protection
</features>

<limitations>
- Requires Chrome/Chromium installed on the system
- Requires kuri-agent CLI tool installed
- Slower than simple HTTP fetch — uses real browser rendering
- Browser state is ephemeral (no persistent sessions between calls)
- Uses a small model sub-agent, so complex reasoning may be limited
</limitations>

<tips>
- Be specific in your prompt about what to do and what information to extract
- For simple content retrieval, prefer kuri_fetch (much faster)
- The agent can take screenshots if visual verification is needed
- Security audit commands (cookies, headers, jwt) are available for security research
</tips>

<examples>
agentic_browser(url: "https://news.ycombinator.com", prompt: "Find the top 5 stories and their point counts")
agentic_browser(prompt: "Go to github.com/charmbracelet/crush, find the latest release version and changelog")
agentic_browser(url: "https://example.com/login", prompt: "Check what security headers are present and audit cookie security flags")
</examples>
`

const browserSystemPrompt = `You are an autonomous browser agent. You control a Chrome browser through the kuri-agent CLI tool. Your job is to navigate web pages, interact with elements, and extract information to answer the user's request.

<tools>
You interact with the browser by outputting kuri-agent commands in a bash tool. The commands available are:

Navigation:
- kuri-agent go <url>          Navigate to a URL
- kuri-agent snap              Take an accessibility snapshot (shows elements with @eN refs)
- kuri-agent snap --text       Text-only snapshot
- kuri-agent snap --semantic   Semantic structure only
- kuri-agent text [selector]   Extract text content

Interaction:
- kuri-agent click <@ref>      Click an element by its @eN reference
- kuri-agent type <@ref> <text> Type text into an element
- kuri-agent fill <@ref> <text> Fill/replace text in an element
- kuri-agent select <@ref> <value> Select a dropdown option
- kuri-agent scroll             Scroll the page down

Information:
- kuri-agent shot [path]       Take a screenshot
- kuri-agent eval <js>         Execute JavaScript in the page
- kuri-agent tabs              List open tabs
- kuri-agent grab <@ref>       Click a link and follow to new page/tab

Security:
- kuri-agent cookies           Dump cookies with security flags
- kuri-agent headers           Audit response security headers
- kuri-agent audit             Full security audit
- kuri-agent jwt               Scan for JWT tokens
- kuri-agent storage all       Dump web storage
</tools>

<workflow>
1. If a URL is provided, navigate to it with "kuri-agent go <url>"
2. Take a snapshot with "kuri-agent snap" to see the page structure
3. Use @eN references from the snapshot to interact with elements
4. After each interaction, take another snapshot to see the updated state
5. Extract the requested information and report it clearly
</workflow>

<rules>
- Always take a snapshot after navigation or interaction to see the current state
- Use @eN references from snapshots for click/type/fill/select actions
- Be methodical: navigate → snapshot → interact → snapshot → extract
- If a page requires scrolling to see more content, use "kuri-agent scroll"
- Report findings clearly with specific data extracted from the page
- If something fails, try an alternative approach before giving up
- Keep your responses concise and focused on the extracted information
</rules>
`

// BrowserParams defines the parameters for the agentic_browser tool.
type BrowserParams struct {
	URL    string `json:"url,omitempty" jsonschema:"description=Starting URL to navigate to (optional — agent can navigate on its own)"`
	Prompt string `json:"prompt" jsonschema:"description=What to accomplish in the browser,required"`
}

// NewBrowserTool creates the agentic_browser tool.
func NewBrowserTool(app *plugin.App, cfg Config) fantasy.AgentTool {
	kuriAgentBin := cfg.KuriAgentPath
	if kuriAgentBin == "" {
		kuriAgentBin = "kuri-agent"
	}

	chromePort := cfg.ChromePort
	if chromePort == 0 {
		chromePort = 9222
	}

	return fantasy.NewAgentTool(
		BrowserToolName,
		browserDescription,
		func(ctx context.Context, params BrowserParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.Prompt == "" {
				return fantasy.NewTextErrorResponse("prompt is required"), nil
			}

			runner := app.SubAgentRunner()
			if runner == nil {
				return fantasy.NewTextErrorResponse("sub-agent runner not available — agentic_browser requires the crush sub-agent system"), nil
			}

			prompt := params.Prompt
			if params.URL != "" {
				prompt = fmt.Sprintf("Navigate to %s and then: %s", params.URL, params.Prompt)
			}

			systemPrompt := fmt.Sprintf("%s\n\n<env>\nkuri-agent binary: %s\nChrome debug port: %d\n</env>", browserSystemPrompt, kuriAgentBin, chromePort)

			result, err := runner.RunSubAgent(ctx, plugin.SubAgentOptions{
				Name:         "browser-agent",
				SystemPrompt: systemPrompt,
				Prompt:       prompt,
				AllowedTools: []string{"bash"},
				Model:        "inherit",
			})
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("browser agent error: %s", err)), nil
			}

			if result == "" {
				return fantasy.NewTextResponse("(browser agent returned no output)"), nil
			}

			return fantasy.NewTextResponse(result), nil
		},
	)
}
