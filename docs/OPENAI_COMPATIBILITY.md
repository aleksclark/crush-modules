# OpenAI model compatibility

## GPT-6 Astra

`gpt-6-astra` works with both provider types:

- `openai`: uses the Responses API (`/responses`) and `max_output_tokens`.
- `openai-compat`: uses Chat Completions (`/chat/completions`) and
  `max_completion_tokens`.

Neither sends the legacy `max_tokens` API field for Astra. Crush's configuration
still uses `max_tokens` / `default_max_tokens`; these are internal output budgets,
not literal API field names. No configuration rename is needed.

Astra is recognized as a reasoning model in both request builders. Selected
reasoning effort is preserved (including `xhigh` and `max`), and unsupported
sampling settings are omitted. Older non-reasoning Chat Completions models keep
their existing token and sampling behavior.

## Dependency pin

All distribution/plugin modules use Fantasy v0.43.1 with a pinned replacement:

```
charm.land/fantasy => github.com/aleksclark/fantasy v0.12.2-0.20260910105032-c654406e1947
```

The fork's pseudo-version is derived from its older published tags, but the
commit is based on upstream **v0.43.1**, not v0.12.1. The patch is
[`c654406e1947`](https://github.com/aleksclark/fantasy/commit/c654406e1947f1edb39176895b3ca3bc6011e9d3)
on `fix/gpt-6-astra`.

Upstream v0.43.0 sends Astra to Chat Completions with `max_tokens`. Upstream
v0.43.1 routes it to Responses, but still fails to classify it as reasoning when
building the request, silently dropping reasoning effort/summary. The pinned
patch fixes that classification for Responses and Chat Completions.

Keep the replacement consistent in the root and plugin `go.mod` files:
GoReleaser builds the root commands, while xcrush propagates replacements from
plugin modules into its generated build module. Updating only the root would
leave `task distro` and individual plugin builds unfixed.

CI also pins the Crush plugin base to
[`991032aee818`](https://github.com/aleksclark/crush/commit/991032aee818e4fabf1e6a2b02d2f27ada4547a6)
on `fix/xcrush-versioned-replacements`. Earlier xcrush builds incorrectly turn a
plugin's versioned replacement into an absolute local path, making `task distro`
fail. Local builds need this commit (or a descendant); see the source checkout
instructions in the README.

Remove the Fantasy replacement once an upstream release passes the regression
tests below, updating the root and every plugin together. Future Crush base
updates must retain the xcrush replacement fix.

## Verification

```sh
# Actual HTTP requests: Responses, Chat Completions, OpenAI-compatible,
# streaming/non-streaming, Astra/GPT-5/legacy GPT-4o.
go test -short ./testutil/...

# Build the release entry point and exercise the compiled CLI.
go build -o dist/crush ./cmd/crush-extended
go test -v -tags e2e ./testutil -run TestGPT6AstraDistro

# Verify xcrush's generated distribution carries the same fix.
task distro
go test -v -tags e2e ./testutil -run TestGPT6AstraDistro
```

The binary test uses isolated configuration and a local HTTP server. It checks
successful streamed output, advertised coding tools, API route, token field,
and reasoning effort. The server rejects Astra requests containing `max_tokens`
to reproduce the original failure. No API key or paid request is needed.

To test another binary, set `CRUSH_BINARY=/absolute/path/to/crush`.
