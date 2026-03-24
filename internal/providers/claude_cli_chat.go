package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// isSessionInUseError checks if the error is a "session already in use" error from Claude CLI.
func isSessionInUseError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "is already in use")
}

// Chat runs the CLI synchronously and returns the final response.
func (p *ClaudeCLIProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	systemPrompt, userMsg, images := extractFromMessages(req.Messages)
	sessionKey := extractStringOpt(req.Options, OptSessionKey)
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}
	if err := validateCLIModel(model); err != nil {
		return nil, err
	}

	unlock := p.lockSession(sessionKey)
	defer unlock()

	workDir := p.ensureWorkDir(sessionKey)
	if systemPrompt != "" {
		p.writeClaudeMD(workDir, systemPrompt)
	}

	cliSessionID := deriveSessionUUID(sessionKey)
	disableTools := extractBoolOpt(req.Options, OptDisableTools)
	bc := bridgeContextFromOpts(req.Options)
	mcpPath := p.resolveMCPConfigPath(ctx, sessionKey, bc)
	args := p.buildArgs(model, workDir, mcpPath, cliSessionID, "json", len(images) > 0, disableTools)

	var stdin *bytes.Reader
	if len(images) > 0 {
		stdin = buildStreamJSONInput(userMsg, images)
	} else {
		args = append(args, "--", userMsg)
	}

	cmd := exec.CommandContext(ctx, p.cliPath, args...)
	cmd.Dir = workDir
	cmd.Env = filterCLIEnv(os.Environ())
	if stdin != nil {
		cmd.Stdin = stdin
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	slog.Debug("claude-cli exec", "cmd", fmt.Sprintf("%s %s", p.cliPath, strings.Join(args, " ")), "workdir", workDir)
	output, err := cmd.Output()
	if err != nil {
		firstErr := fmt.Errorf("claude-cli: %w (stderr: %s)", err, stderr.String())
		// Retry up to 3 times if session is in use (previous process hasn't released the lock yet).
		if isSessionInUseError(firstErr) {
			for retry := 0; retry < 3; retry++ {
				delay := time.Duration(2*(retry+1)) * time.Second
				slog.Info("claude-cli: session in use, retrying", "retry", retry+1, "delay", delay)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(delay):
				}
				cmd2 := exec.CommandContext(ctx, p.cliPath, args...)
				cmd2.Dir = workDir
				cmd2.Env = filterCLIEnv(os.Environ())
				if stdin != nil {
					stdin = buildStreamJSONInput(userMsg, images)
					cmd2.Stdin = stdin
				}
				var stderr2 bytes.Buffer
				cmd2.Stderr = &stderr2
				output2, err2 := cmd2.Output()
				if err2 == nil {
					return parseJSONResponse(output2)
				}
				retryErr := fmt.Errorf("claude-cli: %w (stderr: %s)", err2, stderr2.String())
				if !isSessionInUseError(retryErr) {
					return nil, retryErr
				}
			}
		}
		return nil, firstErr
	}

	return parseJSONResponse(output)
}

// ChatStream runs the CLI with stream-json output, calling onChunk for each text delta.
func (p *ClaudeCLIProvider) ChatStream(ctx context.Context, req ChatRequest, onChunk func(StreamChunk)) (*ChatResponse, error) {
	systemPrompt, userMsg, images := extractFromMessages(req.Messages)
	sessionKey := extractStringOpt(req.Options, OptSessionKey)
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}
	if err := validateCLIModel(model); err != nil {
		return nil, err
	}

	slog.Debug("claude-cli: acquiring session lock", "session_key", sessionKey)
	unlock := p.lockSession(sessionKey)
	slog.Debug("claude-cli: session lock acquired", "session_key", sessionKey)
	defer func() {
		unlock()
		slog.Debug("claude-cli: session lock released", "session_key", sessionKey)
	}()

	workDir := p.ensureWorkDir(sessionKey)
	if systemPrompt != "" {
		p.writeClaudeMD(workDir, systemPrompt)
	}

	cliSessionID := deriveSessionUUID(sessionKey)
	disableTools := extractBoolOpt(req.Options, OptDisableTools)
	bc := bridgeContextFromOpts(req.Options)
	mcpPath := p.resolveMCPConfigPath(ctx, sessionKey, bc)
	args := p.buildArgs(model, workDir, mcpPath, cliSessionID, "stream-json", len(images) > 0, disableTools)

	var stdin *bytes.Reader
	if len(images) > 0 {
		stdin = buildStreamJSONInput(userMsg, images)
	} else {
		args = append(args, "--", userMsg)
	}

	cmd := exec.CommandContext(ctx, p.cliPath, args...)
	cmd.Dir = workDir
	cmd.Env = filterCLIEnv(os.Environ())
	if stdin != nil {
		cmd.Stdin = stdin
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claude-cli stdout pipe: %w", err)
	}

	fullCmd := fmt.Sprintf("%s %s", p.cliPath, strings.Join(args, " "))
	slog.Debug("claude-cli stream exec", "cmd", fullCmd, "workdir", workDir)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claude-cli start: %w", err)
	}

	// Debug log file: only enabled when GOCLAW_DEBUG=1
	var debugFile *os.File
	if os.Getenv("GOCLAW_DEBUG") == "1" {
		debugLogPath := filepath.Join(workDir, "cli-debug.log")
		debugFile, _ = os.OpenFile(debugLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if debugFile != nil {
			fmt.Fprintf(debugFile, "=== CMD: %s\n=== WORKDIR: %s\n=== TIME: %s\n\n", fullCmd, workDir, time.Now().Format(time.RFC3339))
			defer debugFile.Close()
		}
	}

	// Parse stream-json line-by-line
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, StdioScanBufInit), StdioScanBufMax)

	var finalResp ChatResponse
	var contentBuf strings.Builder

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		// Write raw line to debug log
		if debugFile != nil {
			fmt.Fprintf(debugFile, "%s\n", line)
		}

		var ev cliStreamEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			slog.Debug("claude-cli: skip malformed stream line", "error", err)
			continue
		}

		switch ev.Type {
		case "assistant":
			if ev.Message == nil {
				continue
			}
			text, thinking := extractStreamContent(ev.Message)
			if text != "" {
				contentBuf.WriteString(text)
				onChunk(StreamChunk{Content: text})
			}
			if thinking != "" {
				onChunk(StreamChunk{Thinking: thinking})
			}

		case "result":
			if ev.Result != "" {
				finalResp.Content = ev.Result
			} else {
				finalResp.Content = contentBuf.String()
			}
			finalResp.FinishReason = "stop"
			if ev.Subtype == "error" {
				finalResp.FinishReason = "error"
			}
			if ev.Usage != nil {
				finalResp.Usage = &Usage{
					PromptTokens:     ev.Usage.InputTokens,
					CompletionTokens: ev.Usage.OutputTokens,
					TotalTokens:      ev.Usage.InputTokens + ev.Usage.OutputTokens,
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("claude-cli: stream read error: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		if debugFile != nil {
			fmt.Fprintf(debugFile, "\n=== STDERR:\n%s\n=== EXIT ERROR: %v\n", stderrBuf.String(), err)
		}
		// If we got partial content, return it with the error
		if finalResp.Content != "" {
			return &finalResp, nil
		}
		waitErr := fmt.Errorf("claude-cli: %w (stderr: %s)", err, stderrBuf.String())

		// Retry if session is in use (previous CLI process hasn't released the lock).
		if isSessionInUseError(waitErr) {
			for retry := 0; retry < 3; retry++ {
				delay := time.Duration(2*(retry+1)) * time.Second
				slog.Info("claude-cli stream: session in use, retrying", "retry", retry+1, "delay", delay, "session_key", sessionKey)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(delay):
				}
				// Release and re-acquire session lock, then retry the whole ChatStream.
				unlock()
				resp, retryErr := p.ChatStream(ctx, req, onChunk)
				// Re-acquire the lock so the deferred unlock() doesn't panic.
				unlock = p.lockSession(sessionKey)
				if retryErr == nil || !isSessionInUseError(retryErr) {
					return resp, retryErr
				}
			}
		}

		return nil, waitErr
	}
	if debugFile != nil && stderrBuf.Len() > 0 {
		fmt.Fprintf(debugFile, "\n=== STDERR:\n%s\n", stderrBuf.String())
	}

	// Fallback if no "result" event was received
	if finalResp.Content == "" {
		finalResp.Content = contentBuf.String()
		finalResp.FinishReason = "stop"
	}

	onChunk(StreamChunk{Done: true})
	return &finalResp, nil
}
