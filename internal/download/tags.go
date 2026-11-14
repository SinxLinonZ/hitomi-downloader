package download

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type Tag struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}
type TagFacet struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

func normalizeTag(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.ReplaceAll(s, "_", " ")), " "))
}
func tagValue(t Tag) string { return normalizeTag(t.Namespace + ":" + t.Name) }
func flag(raw json.RawMessage) bool {
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		value, e := n.Float64()
		return e == nil && value != 0
	}
	return false
}

// 元の tags は metadata 内に保持し、一覧と検索用の namespace を正規化する。
func parseTags(raw json.RawMessage) []Tag {
	var data struct {
		Tags []struct {
			Tag       string          `json:"tag"`
			Name      string          `json:"name"`
			Type      string          `json:"type"`
			Namespace string          `json:"namespace"`
			Female    json.RawMessage `json:"female"`
			Male      json.RawMessage `json:"male"`
		} `json:"tags"`
	}
	out := []Tag{}
	if json.Unmarshal(raw, &data) != nil {
		return out
	}
	seen := map[string]bool{}
	add := func(ns, name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		value := normalizeTag(ns + ":" + name)
		if !seen[value] {
			out = append(out, Tag{Name: name, Namespace: ns})
			seen[value] = true
		}
	}
	for _, t := range data.Tags {
		name := t.Tag
		if name == "" {
			name = t.Name
		}
		ns := t.Namespace
		if ns == "" {
			ns = t.Type
		}
		if ns == "female" || ns == "male" || ns == "tag" {
			add(ns, name)
		} else if flag(t.Female) || flag(t.Male) {
			if flag(t.Female) {
				add("female", name)
			}
			if flag(t.Male) {
				add("male", name)
			}
		} else {
			add("tag", name)
		}
	}
	return out
}

func hasTag(entry Entry, value string) bool {
	value = normalizeTag(value)
	qualified := strings.Contains(value, ":")
	for _, tag := range entry.Tags {
		if qualified && normalizeTag(tagValue(tag)) == value || !qualified && normalizeTag(tag.Name) == value {
			return true
		}
	}
	return false
}
func matchesQuery(entry Entry, q string) bool {
	text := strings.ToLower(entry.Title + " " + entry.JapaneseTitle + " " + strings.Join(entry.Artists, " ") + " " + fmt.Sprint(entry.ID))
	for _, tag := range entry.Tags {
		text += " " + strings.ToLower(tagValue(tag))
	}
	for _, word := range strings.Fields(strings.ToLower(q)) {
		negative := strings.HasPrefix(word, "-")
		word = strings.TrimPrefix(word, "-")
		if word == "" {
			continue
		}
		match := false
		ns, name, qualified := strings.Cut(word, ":")
		if qualified && (ns == "female" || ns == "male" || ns == "tag") && name != "" {
			match = hasTag(entry, word)
		} else {
			match = strings.Contains(text, strings.ReplaceAll(word, "_", " "))
		}
		if match == negative {
			return false
		}
	}
	return true
}
func tagFacets(counts map[string]int) []TagFacet {
	out := make([]TagFacet, 0, len(counts))
	for value, count := range counts {
		out = append(out, TagFacet{Value: value, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Value < out[j].Value
		}
		return out[i].Count > out[j].Count
	})
	return out
}
