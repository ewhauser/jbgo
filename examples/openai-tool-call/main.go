package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	jbruntime "github.com/ewhauser/jbgo/runtime"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/responses"
)

const (
	exampleModel  = "gpt-4.1-mini"
	bashToolName  = "bash"
	examplePrompt = "Use the bash tool to run this exact script: printf 'hello from bash\\n'. " +
		"After the tool returns, reply with only the raw stdout from the tool."
)

type bashToolArgs struct {
	Script string `json:"script"`
}

type bashToolResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	if os.Getenv("OPENAI_API_KEY") == "" {
		return errors.New("OPENAI_API_KEY is not set")
	}

	client := openai.NewClient()

	first, err := client.Responses.New(ctx, initialResponseParams())
	if err != nil {
		return fmt.Errorf("create initial response: %w", err)
	}

	toolCall, err := firstFunctionCall(first)
	if err != nil {
		return err
	}

	toolOutput, err := executeBashTool(ctx, toolCall.Arguments)
	if err != nil {
		return fmt.Errorf("execute bash tool: %w", err)
	}

	second, err := client.Responses.New(ctx, followupResponseParams(first.ID, toolCall.CallID, toolOutput))
	if err != nil {
		return fmt.Errorf("create follow-up response: %w", err)
	}

	output := second.OutputText()
	if output == "" {
		return errors.New("follow-up response contained no output text")
	}

	fmt.Print(output)
	return nil
}

func initialResponseParams() responses.ResponseNewParams {
	return responses.ResponseNewParams{
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(examplePrompt),
		},
		Model: openai.ResponsesModel(exampleModel),
		ToolChoice: responses.ResponseNewParamsToolChoiceUnion{
			OfFunctionTool: &responses.ToolChoiceFunctionParam{
				Name: bashToolName,
			},
		},
		Tools: []responses.ToolUnionParam{
			{
				OfFunction: &responses.FunctionToolParam{
					Name:        bashToolName,
					Description: openai.String("Run a bash script and return its exit code, stdout, and stderr."),
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"script": map[string]any{
								"type":        "string",
								"description": "The bash script to run.",
							},
						},
						"required":             []string{"script"},
						"additionalProperties": false,
					},
					Strict: openai.Bool(true),
				},
			},
		},
	}
}

func followupResponseParams(previousResponseID, callID, output string) responses.ResponseNewParams {
	return responses.ResponseNewParams{
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: responses.ResponseInputParam{
				responses.ResponseInputItemParamOfFunctionCallOutput(callID, output),
			},
		},
		Model:              openai.ResponsesModel(exampleModel),
		PreviousResponseID: openai.String(previousResponseID),
	}
}

func firstFunctionCall(response *responses.Response) (responses.ResponseFunctionToolCall, error) {
	if response == nil {
		return responses.ResponseFunctionToolCall{}, errors.New("response was nil")
	}
	for _, item := range response.Output {
		if item.Type != "function_call" || item.Name != bashToolName {
			continue
		}
		return item.AsFunctionCall(), nil
	}
	return responses.ResponseFunctionToolCall{}, fmt.Errorf("response did not contain a %q tool call", bashToolName)
}

func executeBashTool(ctx context.Context, arguments string) (string, error) {
	var args bashToolArgs
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("decode tool arguments: %w", err)
	}
	if args.Script == "" {
		return "", errors.New("bash tool call did not include a script")
	}

	rt, err := jbruntime.New(&jbruntime.Config{})
	if err != nil {
		return "", fmt.Errorf("create runtime: %w", err)
	}

	result, err := rt.Run(ctx, &jbruntime.ExecutionRequest{
		Script: args.Script,
	})
	if err != nil {
		return "", fmt.Errorf("run script: %w", err)
	}

	payload, err := json.Marshal(bashToolResult{
		ExitCode: result.ExitCode,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
	})
	if err != nil {
		return "", fmt.Errorf("encode tool output: %w", err)
	}

	return string(payload), nil
}
