# ADK Bash Chat

This example builds a local CLI chatbot with `adk-go` and a persistent `gbash` `bash` tool.

The chat starts with a seeded ops analytics lab in `/home/agent/lab` and a writable scratch area in `/home/agent/work`. The agent can inspect CSV, JSONL, Markdown, and SQLite data with multiple bash tool calls that share the same sandbox session, working directory, and exported shell environment.

## Run

Gemini API:

```bash
export GOOGLE_API_KEY=your-api-key
go run ./examples/adk-bash-chat
```

Vertex AI:

```bash
export GOOGLE_CLOUD_PROJECT=your-project
export GOOGLE_CLOUD_LOCATION=us-central1
go run ./examples/adk-bash-chat --backend=vertex
```

From the `examples/` module, you can also use the bundled Make target:

```bash
cd examples
make run
```

Optional flags:

```bash
go run ./examples/adk-bash-chat --backend=auto --model=gemini-2.5-flash
```

## Sample Prompts

- `Which service looks most suspicious after the last deploy?`
- `Create a markdown summary in /home/agent/work/summary.md and tell me what you wrote.`
- `Now compare that summary against the incidents database and update the markdown file instead of creating a new one.`
- `Which intermediate files have you already created in /home/agent/work?`

Use `/reset` to recreate the ADK conversation and reseed the sandbox.
