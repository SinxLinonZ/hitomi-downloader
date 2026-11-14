package download

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseTagsRetainsNamespacesAndDeduplicates(t *testing.T) {
	tags := parseTags(json.RawMessage(`{"tags":[{"tag":"digital"},{"tag":"glasses","female":"1"},{"tag":"glasses","male":1},{"tag":"glasses","female":true},{"tag":"neutral","female":0.0},{"name":"alt","type":"female"},{"tag":"dual","female":1,"male":1}]}`))
	got := []string{}
	for _, tag := range tags {
		got = append(got, tagValue(tag))
	}
	want := []string{"tag:digital", "female:glasses", "male:glasses", "tag:neutral", "female:alt", "female:dual", "male:dual"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}

func taggedLegacy(t *testing.T, m *Manager, id int, tags []map[string]any) (string, []byte) {
	t.Helper()
	dir, raw := legacyFixture(t, m, id, 1)
	var fields map[string]any
	if e := json.Unmarshal(raw, &fields); e != nil {
		t.Fatal(e)
	}
	fields["tags"] = tags
	raw, e := json.Marshal(fields)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "metadata.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	return dir, raw
}

func TestDownloadedSearchCombinesTextNamespacesAndTagFilters(t *testing.T) {
	m, _, _ := fixture(t)
	taggedLegacy(t, m, 123, []map[string]any{{"tag": "digital"}, {"tag": "glasses", "female": 1}, {"tag": "long hair", "female": "1"}})
	taggedLegacy(t, m, 124, []map[string]any{{"tag": "digital"}, {"tag": "glasses", "male": 1}})
	taggedLegacy(t, m, 125, nil)
	_ = indexReady(t, m)
	for _, tc := range []struct {
		q     string
		tags  []string
		total int
	}{{"", nil, 3}, {"Legacy SOME artist", nil, 3}, {"123", nil, 1}, {"digital", nil, 2}, {"female:glasses", nil, 1}, {"male:glasses", nil, 1}, {"female:long_hair", nil, 1}, {"-tag:digital", nil, 1}, {"digital -female:glasses", nil, 1}, {"", []string{"tag:digital", "female:glasses"}, 1}, {"", []string{"female:glasses", "male:glasses"}, 0}, {"", []string{"glasses"}, 2}} {
		got, e := m.Library(1, 20, tc.q, "all", tc.tags...)
		if e != nil || got.Total != tc.total {
			t.Errorf("q=%q tags=%v total=%d want=%d %v", tc.q, tc.tags, got.Total, tc.total, e)
		}
	}
	result, e := m.Library(1, 20, "", "legacy", "female:glasses")
	if e != nil {
		t.Fatal(e)
	}
	counts := map[string]int{}
	for _, tag := range result.Tags {
		counts[tag.Value] = tag.Count
	}
	if counts["tag:digital"] != 2 || counts["male:glasses"] != 1 || counts["female:long hair"] != 1 {
		t.Fatalf("%v", counts)
	}
}

func TestMigrationPreservesTagsAndKeepsThemFilterable(t *testing.T) {
	m, _, _ := fixture(t)
	tags := []map[string]any{{"tag": "digital", "url": "/tag/digital-all-1.html"}, {"tag": "glasses", "female": 1}, {"tag": "glasses", "male": "1"}}
	dir, _ := taggedLegacy(t, m, 123, tags)
	if e := m.Migrate(dir); e != nil {
		t.Fatal(e)
	}
	if task := await(t, m, 123); task.State != "completed" {
		t.Fatalf("%+v", task)
	}
	meta, _, e := readMetadata(dir)
	if e != nil {
		t.Fatal(e)
	}
	var saved struct {
		Tags []map[string]any `json:"tags"`
	}
	if e = json.Unmarshal(meta.Gallery, &saved); e != nil {
		t.Fatal(e)
	}
	expected, _ := json.Marshal(tags)
	actual, _ := json.Marshal(saved.Tags)
	if !bytes.Equal(expected, actual) {
		t.Fatalf("tags changed: %s != %s", expected, actual)
	}
	_ = indexReady(t, m)
	result, e := m.Library(1, 20, "", "native", "male:glasses")
	if e != nil || result.Total != 1 || len(result.Entries[0].Tags) != 3 {
		t.Fatalf("%+v %v", result, e)
	}
}

func TestNewDownloadSavesOriginalTags(t *testing.T) {
	m, _, _ := fixture(t)
	original := m.client.HTTP.Transport
	m.client.HTTP.Transport = rt(func(r *http.Request) (*http.Response, error) {
		response, e := original.RoundTrip(r)
		if e != nil || !strings.Contains(r.URL.Path, "/galleries/") {
			return response, e
		}
		body, e := io.ReadAll(response.Body)
		response.Body.Close()
		if e != nil {
			return nil, e
		}
		body = bytes.Replace(body, []byte(`"files":`), []byte(`"tags":[{"tag":"digital","url":"/original-tag-url"},{"tag":"glasses","female":"1"}],"files":`), 1)
		response.Body = io.NopCloser(bytes.NewReader(body))
		response.ContentLength = int64(len(body))
		return response, nil
	})
	if e := m.Start(42, "", "download"); e != nil {
		t.Fatal(e)
	}
	task := await(t, m, 42)
	if task.State != "completed" {
		t.Fatalf("%+v", task)
	}
	meta, _, e := readMetadata(task.Directory)
	if e != nil {
		t.Fatal(e)
	}
	var saved struct {
		Tags []map[string]any `json:"tags"`
	}
	if e = json.Unmarshal(meta.Gallery, &saved); e != nil {
		t.Fatal(e)
	}
	if len(saved.Tags) != 2 || saved.Tags[0]["url"] != "/original-tag-url" || saved.Tags[1]["female"] != "1" {
		t.Fatalf("%+v", saved.Tags)
	}
	_ = indexReady(t, m)
	result, e := m.Library(1, 20, "female:glasses", "native")
	if e != nil || result.Total != 1 {
		t.Fatalf("%+v %v", result, e)
	}
}
