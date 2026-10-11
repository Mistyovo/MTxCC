// Package harness 实现 agent 会话循环（无业务语义）：
// transcript 持久化在此，主循环/压缩/收尾见同包其他文件。
package harness

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"mtx/internal/llm"
)

// TranscriptStore 是会话原始对话的追加式存储（spec §3.7：JSONL，session 命名规则见 launch）。
type TranscriptStore interface {
	Append(sessionID string, m llm.Message) error
	Load(sessionID string) ([]llm.Message, error)
}

// NewFileTranscript 以 dir 为根目录的文件实现（<dir>/<sessionID>.jsonl，每行一条消息）。
func NewFileTranscript(dir string) TranscriptStore { return &fileTranscript{dir: dir} }

type fileTranscript struct{ dir string }

func (f *fileTranscript) path(sessionID string) string {
	return filepath.Join(f.dir, sessionID+".jsonl")
}

func (f *fileTranscript) Append(sessionID string, m llm.Message) error {
	if err := os.MkdirAll(f.dir, 0o750); err != nil {
		return err
	}
	line, err := json.Marshal(m)
	if err != nil {
		return err
	}
	fp, err := os.OpenFile(f.path(sessionID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	defer fp.Close()
	if _, err := fp.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("追加 transcript: %w", err)
	}
	return nil
}

func (f *fileTranscript) Load(sessionID string) ([]llm.Message, error) {
	fp, err := os.Open(f.path(sessionID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer fp.Close()

	var msgs []llm.Message
	sc := bufio.NewScanner(fp)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var m llm.Message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			return nil, fmt.Errorf("解析 transcript 行: %w", err)
		}
		msgs = append(msgs, m)
	}
	return msgs, sc.Err()
}

// SanitizeTail 清理恢复历史尾部的悬空 tool_use：
// 从尾向前找最后一条含未配对 tool_use 的 assistant 消息，删除其后所有消息，
// 并删该消息内未配对的 tool_use 块（块全删则整条消息删除）——
// 保证 Resume 后重新发送不因缺 tool_result 被服务端拒绝（spec Review Focus 3）。
func SanitizeTail(msgs []llm.Message) []llm.Message {
	// 从尾部回溯：找到第一条"其后存在悬空 tool_use"的边界。
	// 简化实现：累计全部配对，再从尾删除悬空。
	answered := map[string]bool{}
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult {
				answered[b.ToolUseID] = true
			}
		}
	}
	// 从尾向前删除：悬空 tool_use 所在消息及其后的所有消息。
	cut := len(msgs)
	for i := len(msgs) - 1; i >= 0; i-- {
		dangling := false
		for _, b := range msgs[i].Blocks {
			if b.Type == llm.BlockToolUse && !answered[b.ID] {
				dangling = true
				break
			}
		}
		if !dangling {
			break
		}
		cut = i
	}
	if cut == len(msgs) {
		return msgs
	}
	// cut 处消息可能部分配对：保留已配对块。
	kept := make([]llm.Message, 0, cut+1)
	kept = append(kept, msgs[:cut]...)
	partial := msgs[cut]
	var blocks []llm.Block
	for _, b := range partial.Blocks {
		if b.Type == llm.BlockToolUse && !answered[b.ID] {
			continue
		}
		blocks = append(blocks, b)
	}
	if len(blocks) > 0 {
		kept = append(kept, llm.Message{Role: partial.Role, Blocks: blocks})
	}
	return kept
}
