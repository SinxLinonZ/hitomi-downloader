package download

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func seedTask(t *testing.T, m *Manager, id int, state string) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	task := &Task{ID: id, Root: m.root, State: state, Operation: "download", Errors: map[int]string{}}
	m.tasks[id] = task
	if e := m.save(task); e != nil {
		t.Fatal(e)
	}
}

func TestControlAllPauseAndImmediatelyResume(t *testing.T) {
	m, _, _ := fixture(t)
	_ = m.taskLimit.acquire(context.Background())
	_ = m.taskLimit.acquire(context.Background())
	for _, id := range []int{42, 43} {
		if e := m.Start(id, "", "download"); e != nil {
			t.Fatal(e)
		}
	}
	seedTask(t, m, 44, "failed")
	seedTask(t, m, 45, "cancelled")
	seedTask(t, m, 46, "completed")
	paused, e := m.ControlAll(context.Background(), "pause")
	if e != nil || paused.Affected != 2 || len(paused.Errors) != 0 {
		t.Fatalf("%+v %v", paused, e)
	}
	resumed, e := m.ControlAll(context.Background(), "resume")
	if e != nil || resumed.Affected != 4 || len(resumed.Errors) != 0 {
		t.Fatalf("%+v %v", resumed, e)
	}
	for _, task := range m.List() {
		if task.ID == 46 {
			if task.State != "completed" {
				t.Fatal(task)
			}
			continue
		}
		if task.State != "queued" {
			t.Fatalf("unexpected task state: %+v", task)
		}
	}
	paused, e = m.ControlAll(context.Background(), "pause")
	if e != nil || paused.Affected != 4 {
		t.Fatalf("%+v %v", paused, e)
	}
	for _, id := range []int{42, 43, 44, 45} {
		if task := await(t, m, id); task.State != "paused" {
			t.Fatal(task)
		}
		b, e := os.ReadFile(filepath.Join(m.state, "tasks", strconv.Itoa(id)+".json"))
		if e != nil {
			t.Fatal(e)
		}
		var task Task
		if e = json.Unmarshal(b, &task); e != nil || task.State != "paused" {
			t.Fatalf("%+v %v", task, e)
		}
	}
}

func TestRemoveCompletedKeepsDownloadsAndDoesNotReloadTask(t *testing.T) {
	m, _, _ := fixture(t)
	if e := m.Start(42, "", "download"); e != nil {
		t.Fatal(e)
	}
	task := await(t, m, 42)
	if task.State != "completed" {
		t.Fatalf("%+v", task)
	}
	metadataPath := filepath.Join(task.Directory, "metadata.json")
	metadata, e := os.ReadFile(metadataPath)
	if e != nil {
		t.Fatal(e)
	}
	imagePath := filepath.Join(task.Directory, "0001.png")
	image, e := os.ReadFile(imagePath)
	if e != nil {
		t.Fatal(e)
	}
	seedTask(t, m, 43, "paused")
	result, e := m.RemoveCompleted(context.Background())
	if e != nil || result.Affected != 1 || len(result.Errors) != 0 {
		t.Fatalf("%+v %v", result, e)
	}
	if _, e = os.Stat(filepath.Join(m.state, "tasks", "42.json")); !os.IsNotExist(e) {
		t.Fatal("completed task record still exists", e)
	}
	afterMetadata, _ := os.ReadFile(metadataPath)
	afterImage, _ := os.ReadFile(imagePath)
	if !bytes.Equal(metadata, afterMetadata) || !bytes.Equal(image, afterImage) {
		t.Fatal("removing task changed downloaded content")
	}
	if e = m.Control(42, "resume"); e == nil {
		t.Fatal("stale action recreated removed task")
	}
	reloaded, e := New(m.client, m.Root(), m.state)
	if e != nil {
		t.Fatal(e)
	}
	defer reloaded.Close()
	tasks := reloaded.List()
	if len(tasks) != 1 || tasks[0].ID != 43 {
		t.Fatalf("removed task came back on restart: %+v", tasks)
	}
	entries, _, e := reloaded.Scan()
	if e != nil || len(entries) != 1 || entries[0].ID != 42 {
		t.Fatalf("downloaded artwork disappeared: %+v %v", entries, e)
	}
}

func TestRemoveCompletedRetainsTaskWhenRecordCannotBeRemoved(t *testing.T) {
	m, _, _ := fixture(t)
	seedTask(t, m, 42, "completed")
	path := filepath.Join(m.state, "tasks", "42.json")
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(path, "keep"), []byte("not a task record"), 0600); e != nil {
		t.Fatal(e)
	}
	result, e := m.RemoveCompleted(context.Background())
	if e != nil || result.Affected != 0 || result.Errors[42] == "" || len(m.List()) != 1 {
		t.Fatalf("%+v %v", result, e)
	}
	if _, e = os.Stat(filepath.Join(path, "keep")); e != nil {
		t.Fatal("removed unrelated contents", e)
	}
}

func TestRemoveCompletedWaitCanBeCancelled(t *testing.T) {
	m, _, _ := fixture(t)
	seedTask(t, m, 42, "completed")
	settled := make(chan struct{})
	m.mu.Lock()
	m.settled[42] = settled
	m.cancel[42] = func() {}
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result, e := m.RemoveCompleted(ctx)
	if !errors.Is(e, context.DeadlineExceeded) || result.Affected != 0 {
		t.Fatalf("%+v %v", result, e)
	}
	if _, e = os.Stat(filepath.Join(m.state, "tasks", "42.json")); e != nil {
		t.Fatal("removed active worker record", e)
	}
	m.mu.Lock()
	delete(m.cancel, 42)
	delete(m.settled, 42)
	close(settled)
	m.mu.Unlock()
	result, e = m.RemoveCompleted(context.Background())
	if e != nil || result.Affected != 1 {
		t.Fatalf("%+v %v", result, e)
	}
}

func TestPauseAllCancelsRunningAndQueuedWorkers(t *testing.T) {
	m, _, _ := fixture(t)
	_ = m.taskLimit.acquire(context.Background())
	old := m.client.HTTP.Transport
	entered := make(chan struct{}, 2)
	m.client.HTTP.Transport = rt(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, ".png") {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		return old.RoundTrip(r)
	})
	if e := m.Start(42, "", "download"); e != nil {
		t.Fatal(e)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("running download did not start")
	}
	if e := m.Start(43, "", "download"); e != nil {
		t.Fatal(e)
	}
	result, e := m.ControlAll(context.Background(), "pause")
	if e != nil || result.Affected != 2 || len(result.Errors) != 0 {
		t.Fatalf("%+v %v", result, e)
	}
	for _, id := range []int{42, 43} {
		if task := await(t, m, id); task.State != "paused" {
			t.Fatal(task)
		}
	}
}

func TestResumeAllReportsForeignRootWithoutBlockingOtherTasks(t *testing.T) {
	m, _, _ := fixture(t)
	_ = m.taskLimit.acquire(context.Background())
	_ = m.taskLimit.acquire(context.Background())
	seedTask(t, m, 42, "paused")
	seedTask(t, m, 43, "paused")
	m.mu.Lock()
	m.tasks[43].Root = t.TempDir()
	m.mu.Unlock()
	result, e := m.ControlAll(context.Background(), "resume")
	if e != nil || result.Affected != 1 || result.Errors[43] == "" {
		t.Fatalf("%+v %v", result, e)
	}
}
