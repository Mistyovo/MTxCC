// Package notify 实现通知推送（spec §10，ARTEX IM 推送对标）：
// 事件 → 通道路由（通配匹配）→ 按通道类型格式化 → webhook 投递。
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"sync"
	"time"
)

// Event 是通知事件（kind 形如 finding.new / task.done / approval.pending）。
type Event struct {
	Kind   string `json:"kind"`
	TaskID int64  `json:"task_id"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// Channel 是投递通道（webhook/dingtalk/feishu/wecom/tg 共用 HTTP webhook 语义）。
type Channel struct {
	Type   string   `json:"type"` // webhook|dingtalk|feishu|wecom|tg
	URL    string   `json:"url"`
	Name   string   `json:"name"`
	Events []string `json:"events"` // 通配：finding.* / task.done / *（空=全部）
}

// Notifier 管理通道并路由投递。
type Notifier struct {
	mu       sync.RWMutex
	channels []Channel
	client   *http.Client
}

// New 建通知器。
func New() *Notifier {
	return &Notifier{client: &http.Client{Timeout: 10 * time.Second}}
}

// Register 注册通道。
func (n *Notifier) Register(c Channel) error {
	if c.URL == "" {
		return fmt.Errorf("通道 %q 缺 URL", c.Name)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.channels = append(n.channels, c)
	return nil
}

// Deliver 路由投递事件（匹配通道的 events 通配；失败不阻塞主流程）。
func (n *Notifier) Deliver(ev Event) error {
	n.mu.RLock()
	chs := append([]Channel(nil), n.channels...)
	n.mu.RUnlock()

	var lastErr error
	delivered := 0
	for _, c := range chs {
		if !matchEvents(c.Events, ev.Kind) {
			continue
		}
		if err := n.send(c, ev); err != nil {
			lastErr = err
			continue
		}
		delivered++
	}
	if delivered == 0 && lastErr != nil {
		return lastErr
	}
	return nil
}

// matchEvents 通配匹配（finding.* 匹配 finding.new；* 匹配全部）。
func matchEvents(patterns []string, kind string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		if ok, _ := path.Match(p, kind); ok {
			return true
		}
	}
	return false
}

// send 按通道类型格式化并 POST。
func (n *Notifier) send(c Channel, ev Event) error {
	var payload any
	switch c.Type {
	case "dingtalk":
		payload = DingTalkMessage(ev)
	case "feishu":
		payload = FeishuMessage(ev)
	case "wecom":
		payload = WeComMessage(ev)
	default: // webhook / tg
		payload = map[string]any{"text": fmt.Sprintf("[%s] %s\n%s", ev.Kind, ev.Title, ev.Detail)}
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, c.URL, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("通道 %s HTTP %d", c.Name, resp.StatusCode)
	}
	return nil
}

// DingTalkMessage 钉钉 markdown 消息。
func DingTalkMessage(ev Event) map[string]any {
	text := fmt.Sprintf("### %s\n\n**事件**：%s\n**任务**：#%d\n\n%s", ev.Title, ev.Kind, ev.TaskID, ev.Detail)
	return map[string]any{
		"msgtype": "markdown",
		"markdown": map[string]any{"title": ev.Title, "text": text},
	}
}

// FeishuMessage 飞书 text 消息。
func FeishuMessage(ev Event) map[string]any {
	return map[string]any{
		"msg_type": "text",
		"content":  map[string]any{"text": fmt.Sprintf("[%s] %s\n%s", ev.Kind, ev.Title, ev.Detail)},
	}
}

// WeComMessage 企业微信 markdown 消息。
func WeComMessage(ev Event) map[string]any {
	return map[string]any{
		"msgtype": "markdown",
		"markdown": map[string]any{
			"content": fmt.Sprintf("### %s\n%s\n任务 #%d", ev.Title, ev.Detail, ev.TaskID),
		},
	}
}
