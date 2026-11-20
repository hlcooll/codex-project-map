package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

type rpcEnvelope struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}
type codexRPC struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	messages chan rpcEnvelope
	ended    chan struct{}
	writeMu  sync.Mutex
	sequence int
	readErr  error
}

func openCodexRPC(ctx context.Context, binary, cwd string, notify func(rpcEnvelope)) (*codexRPC, error) {
	cmd := exec.Command(binary, "app-server", "--listen", "stdio://")
	cmd.Dir = cwd
	return openCodexCommand(ctx, cmd, notify)
}
func openCodexCommand(ctx context.Context, cmd *exec.Cmd, notify func(rpcEnvelope)) (*codexRPC, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	// Diagnostics may include provider configuration; never forward stderr to the browser.
	cmd.Stderr = io.Discard
	c := &codexRPC{cmd: cmd, stdin: stdin, messages: make(chan rpcEnvelope, 128), ended: make(chan struct{})}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		defer close(c.messages)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 65536), 8<<20)
		for scanner.Scan() {
			var msg rpcEnvelope
			if json.Unmarshal(scanner.Bytes(), &msg) != nil {
				c.readErr = errors.New("Codex 返回了无法解析的消息")
				return
			}
			select {
			case c.messages <- msg:
			case <-c.ended:
				return
			}
		}
		c.readErr = scanner.Err()
	}()
	_, err = c.call(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "project_map", "title": "项目图谱", "version": "0.2.0"}}, notify)
	if err == nil {
		err = c.write(map[string]any{"method": "initialized", "params": map[string]any{}})
	}
	if err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}
func (c *codexRPC) write(value any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return json.NewEncoder(c.stdin).Encode(value)
}
func (c *codexRPC) next(ctx context.Context, notify func(rpcEnvelope)) (rpcEnvelope, error) {
	select {
	case <-ctx.Done():
		return rpcEnvelope{}, ctx.Err()
	case msg, ok := <-c.messages:
		if !ok {
			if c.readErr != nil {
				return msg, c.readErr
			}
			return msg, errors.New("Codex 连接已结束")
		}
		if msg.Method != "" {
			if len(msg.ID) > 0 {
				// This client never silently approves extra permissions or guesses user answers.
				switch msg.Method {
				case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
					_ = c.write(map[string]any{"id": msg.ID, "result": map[string]any{"decision": "decline"}})
				default:
					_ = c.write(map[string]any{"id": msg.ID, "error": map[string]any{"code": -32601, "message": "此工作台暂不支持交互请求，请在任务要求中补充信息后重试。"}})
				}
			}
			if notify != nil {
				notify(msg)
			}
		}
		return msg, nil
	}
}
func (c *codexRPC) call(ctx context.Context, method string, params any, notify func(rpcEnvelope)) (json.RawMessage, error) {
	c.sequence++
	id := c.sequence
	if err := c.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for {
		msg, err := c.next(ctx, notify)
		if err != nil {
			return nil, err
		}
		var received int
		_ = json.Unmarshal(msg.ID, &received)
		if msg.Method != "" || received != id {
			continue
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, clipActivity(msg.Error.Message, 600))
		}
		return msg.Result, nil
	}
}
func (c *codexRPC) interrupt(thread, turn string) {
	if thread != "" && turn != "" {
		_ = c.write(map[string]any{"id": 999999, "method": "turn/interrupt", "params": map[string]any{"threadId": thread, "turnId": turn}})
	}
}
func (c *codexRPC) close() {
	close(c.ended)
	_ = c.stdin.Close()
	done := make(chan struct{})
	go func() { _ = c.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = c.cmd.Process.Kill()
		<-done
	}
}
