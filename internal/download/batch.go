package download

import (
	"context"
	"hitomi-downloader/internal/i18n"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

type TaskBatchResult struct {
	Affected int            `json:"affected"`
	Skipped  int            `json:"skipped"`
	Errors   map[int]string `json:"errors"`
}

func newTaskBatchResult() TaskBatchResult { return TaskBatchResult{Errors: map[int]string{}} }

// 呼び出し元が m.mu を保持する。
func (m *Manager) taskIDs() []int {
	ids := make([]int, 0, len(m.tasks))
	for id := range m.tasks {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}
func awaitSettled(ctx context.Context, done <-chan struct{}) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if done == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (m *Manager) ControlAll(ctx context.Context, action string) (TaskBatchResult, error) {
	result := newTaskBatchResult()
	if action != "pause" && action != "resume" {
		return result, i18n.Errorf("无效批量操作")
	}
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return result, i18n.Errorf("服务正在停止")
	}
	ids := m.taskIDs()
	m.mu.Unlock()
	for _, id := range ids {
		if e := ctx.Err(); e != nil {
			return result, e
		}
		m.mu.Lock()
		if m.closing {
			m.mu.Unlock()
			return result, i18n.Errorf("服务正在停止")
		}
		task := m.tasks[id]
		if task == nil {
			result.Skipped++
			m.mu.Unlock()
			continue
		}
		if action == "pause" {
			if task.State != "queued" && task.State != "running" {
				result.Skipped++
				m.mu.Unlock()
				continue
			}
			task.State = "paused"
			task.UpdatedAt = time.Now()
			if cancel := m.cancel[id]; cancel != nil {
				cancel()
			}
			if e := m.save(task); e != nil {
				result.Errors[id] = e.Error()
			} else {
				result.Affected++
			}
			m.mu.Unlock()
			continue
		}
		if task.State != "paused" && task.State != "failed" && task.State != "cancelled" {
			result.Skipped++
			m.mu.Unlock()
			continue
		}
		done := m.settled[id]
		m.mu.Unlock()
		// キャンセルされた worker の最終保存が終わってから、同じ ID を再開する。
		if e := awaitSettled(ctx, done); e != nil {
			return result, e
		}
		m.mu.Lock()
		current := m.tasks[id]
		if e := ctx.Err(); e != nil {
			m.mu.Unlock()
			return result, e
		}
		if current != task || (current.State != "paused" && current.State != "failed" && current.State != "cancelled") || m.cancel[id] != nil {
			result.Skipped++
			m.mu.Unlock()
			continue
		}
		if e := m.startLocked(id, "", ""); e != nil {
			result.Errors[id] = e.Error()
		} else {
			result.Affected++
		}
		m.mu.Unlock()
	}
	return result, nil
}

func (m *Manager) RemoveCompleted(ctx context.Context) (TaskBatchResult, error) {
	result := newTaskBatchResult()
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return result, i18n.Errorf("服务正在停止")
	}
	ids := m.taskIDs()
	m.mu.Unlock()
	for _, id := range ids {
		if e := ctx.Err(); e != nil {
			return result, e
		}
		m.mu.Lock()
		task := m.tasks[id]
		if task == nil || task.State != "completed" {
			result.Skipped++
			m.mu.Unlock()
			continue
		}
		done := m.settled[id]
		m.mu.Unlock()
		if e := awaitSettled(ctx, done); e != nil {
			return result, e
		}
		m.mu.Lock()
		if e := ctx.Err(); e != nil {
			m.mu.Unlock()
			return result, e
		}
		if m.closing {
			m.mu.Unlock()
			return result, i18n.Errorf("服务正在停止")
		}
		current := m.tasks[id]
		if current != task || current.State != "completed" || m.cancel[id] != nil {
			result.Skipped++
			m.mu.Unlock()
			continue
		}
		// ダウンロード先には触れず、アプリのタスク記録だけを削除する。
		path := filepath.Join(m.state, "tasks", strconv.Itoa(id)+".json")
		if e := os.Remove(path); e != nil && !os.IsNotExist(e) {
			result.Errors[id] = e.Error()
		} else {
			delete(m.tasks, id)
			result.Affected++
		}
		m.mu.Unlock()
	}
	return result, nil
}
